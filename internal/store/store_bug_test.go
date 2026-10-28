package store

// ============================================================================
// 缺陷复现测试（预期红）：以下 TestBug_* 断言"正确行为"，当前生产代码存在
// 确认缺陷，运行时会失败。对应 docs/review/01-review-store-templates.md 发现清单：
//   1. store.go:48  [P2] 敏感明文以 "enc:v1:" 开头被跳过加密原样入库，读回解密失败
//   2. store.go:204 [P1] 一条坏行使 ListEntries 整体报错，无隔离/降级
//   3. store.go:318 [P1] RotateAllEntries 漏掉 settings 表中同 Cipher 加密的 LLM API Key
// 修复生产代码后这些测试应转绿；断言不弱化。
// ============================================================================

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/lpx0312/keyHive/internal/crypto"
	"github.com/lpx0312/keyHive/internal/model"
)

// Bug1 [P2, store.go:48]：用户真实明文恰好以 "enc:v1:" 开头（如从导出文件复制来的
// 密文样串）时，encryptFieldsWith 因 IsEncrypted 前缀判断跳过加密、原样入库；
// 读取时 decryptFields 对带前缀串强制解密必然失败——条目自我砖化。
// 正确行为：该值应被正常加密，round-trip 后读回原文。
func TestBug_PlaintextWithEncPrefixCorrupted(t *testing.T) {
	st := newTestStore(t)
	plain := "enc:v1:" + base64.StdEncoding.EncodeToString([]byte("这看起来像密文但其实是用户明文"))
	e := model.Entry{Title: "前缀陷阱", Category: "misc",
		Fields: []model.Field{
			{Key: "host", Type: "text", Value: "h1"},
			{Key: "password", Type: "text", IsSecret: true, Value: plain},
		}}
	if err := st.CreateEntry(&e); err != nil {
		t.Fatalf("创建应成功: %v", err)
	}

	got, err := st.GetEntry(e.ID)
	if err != nil {
		t.Fatalf("正确行为：以 enc:v1: 开头的明文应被正常加密并可读回；当前读回即失败: %v", err)
	}
	if got.FieldByKey("password").Value != plain {
		t.Fatalf("round-trip 后应读回原文，实际 %q", got.FieldByKey("password").Value)
	}

	// 落库值不应等于明文本身（必须经过加密，而非靠读取侧兜底）
	var raw string
	st.DB.QueryRow(`SELECT fields FROM entries WHERE id = ?`, e.ID).Scan(&raw)
	if v := extractValue(t, raw, "password"); v == plain {
		t.Fatal("正确行为：该值应被加密后落库，当前明文原样入库")
	}
}

// Bug2 [P1, store.go:204]：库中一条坏行（解密失败）令 ListEntries 整体报错，
// 全库不可列出、导出与 AI 检索连带失败。正确行为：列表容错——跳过坏行并仍返回其余条目。
func TestBug_ListEntriesSurvivesBadRow(t *testing.T) {
	st := newTestStore(t)
	good1 := seedEntry(t, st, "好数据1", "misc",
		model.Field{Key: "url", Type: "url", Value: "https://a.corp"},
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "good-secret-1"})
	good2 := seedEntry(t, st, "好数据2", "misc",
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "good-secret-2"})
	bad := seedEntry(t, st, "坏数据", "misc",
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "will-be-broken"})
	// 直插一条畸形 enc:v1: 密文行（base64 非法，Decrypt 必败）
	st.DB.Exec(`UPDATE entries SET fields = ? WHERE id = ?`,
		`[{"key":"password","type":"text","is_secret":true,"value":"enc:v1:$$$不是base64$$$"}]`, bad.ID)

	list, err := st.ListEntries("", "")
	if err != nil {
		t.Fatalf("正确行为：单条坏行应被跳过而不是拖垮整个列表；当前整体报错: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("正确行为：应返回其余 %d 条好数据，实际 %d 条", 2, len(list))
	}
	seen := map[int64]bool{}
	for _, e := range list {
		seen[e.ID] = true
		if e.ID == bad.ID {
			t.Fatal("坏行不应出现在结果中")
		}
	}
	if !seen[good1.ID] || !seen[good2.ID] {
		t.Fatal("好数据条目不应丢失")
	}
	// 返回的好条目敏感值仍是可解密的明文
	for _, e := range list {
		if f := e.FieldByKey("password"); f != nil && f.Value == "" {
			t.Fatalf("条目 %d 敏感值解密异常", e.ID)
		}
	}
}

// Bug3 [P1, store.go:318]：RotateAllEntries 只重加密 entries + key_check，
// 漏掉 settings 表中用同一 Cipher 加密的 LLM API Key（存法参考
// internal/api/aichat_api.go:109：s.Store.Cipher.Encrypt 后写 settings 键 ai_llm_api_key）。
// 正确行为：轮换后该配置仍能用新密钥解密还原。
func TestBug_RotateMissesSettingsSecret(t *testing.T) {
	st := newTestStore(t)
	seedEntry(t, st, "普通条目", "misc",
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "entry-secret"})

	// 按 aichat_api.go 的存法：用当前 Cipher 加密 LLM API Key 写入 settings
	const llmPlain = "sk-llm-987654321"
	enc, err := st.cipher().Encrypt(llmPlain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('ai_llm_api_key', ?)`, enc); err != nil {
		t.Fatal(err)
	}

	// 新随机密钥轮换（与 store_test.go TestRotateAllEntries 同构）
	raw := make([]byte, 32)
	rand.Read(raw)
	newCipher, err := crypto.New(crypto.DeriveKeyText(base64.StdEncoding.EncodeToString(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RotateAllEntries(newCipher); err != nil {
		t.Fatalf("轮换本身应成功: %v", err)
	}
	// entries 侧轮换确实生效（内存密钥已切换，条目可读）
	if _, err := st.GetEntry(1); err != nil {
		t.Fatalf("条目侧轮换应正常: %v", err)
	}

	var stored string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'ai_llm_api_key'`).Scan(&stored)
	plain, err := newCipher.Decrypt(stored)
	if err != nil {
		t.Fatalf("正确行为：轮换后 settings 中的 LLM API Key 应能用新密钥解密还原；当前解密失败（旧密钥密文失联）: %v", err)
	}
	if plain != llmPlain {
		t.Fatalf("解密结果应还原原文 %q，实际 %q", llmPlain, plain)
	}
}
