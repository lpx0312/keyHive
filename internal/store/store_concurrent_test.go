package store

// 【回归锚点｜轮换与并发写 TOCTOU 的进程级互斥】
// 对应 docs/review/04-role-review-r1-go.md #10 并发窗口与 03-refactor-plans A1 最小修复：
// RotateAllEntries 是"预读旧密文 → 预计算新密文 → 事务重写 → 换内存密钥"，与并发
// CreateEntry 交错时，预读之后、提交之前用旧钥加密落库的条目不会被重加密，换钥后
// 永久不可解密（数据丢失，而非仅报错）。连接池 MaxOpenConns(1) 只串行化语句，
// 不串行化逻辑上的读-改-写。
// 修复：Store.writeMu 让条目写与轮换互斥——任何写要么完整发生在轮换预读前（会被
// 重加密），要么完整发生在换钥后（用新钥）。
// 断言与调度时序无关（确定性）：并发结束后全部条目必须能解密回原始明文，且条数
// 一个不少（坏行会被 ListEntries 静默跳过，条数校验兜住"被跳过"的静默丢失）。

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"

	"github.com/lpx0312/keyHive/internal/crypto"
	"github.com/lpx0312/keyHive/internal/model"
)

// newCipherForTest 随机派生一个全新 Cipher（与 store_test.go 轮换用例同构）
func newCipherForTest(t *testing.T) *crypto.Cipher {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	c, err := crypto.New(crypto.DeriveKeyText(base64.StdEncoding.EncodeToString(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestConcurrentCreateAndRotateRoundTrip：并发 goroutine 各自 CreateEntry 的同时
// RotateAllEntries；结束后全部条目用最终密钥 round-trip 解密出原明文
func TestConcurrentCreateAndRotateRoundTrip(t *testing.T) {
	st := newTestStore(t)

	// 期望明文表：title → password 明文（写前登记，断言只查这张表）
	want := map[string]string{}

	// 先造一批旧钥条目：拉长轮换的预读/预计算窗口，提高与并发写的交错概率
	const initial = 40
	for i := 0; i < initial; i++ {
		title := fmt.Sprintf("init-%02d", i)
		e := model.Entry{Title: title, Category: "misc", Fields: []model.Field{
			{Key: "password", Type: "text", IsSecret: true, Value: "pw-" + title}}}
		if err := st.CreateEntry(&e); err != nil {
			t.Fatal(err)
		}
		want[title] = "pw-" + title
	}

	// 并发条目的期望明文也预先登记（goroutine 内不能写共享 map）
	const workers, perWorker = 4, 15
	for w := 0; w < workers; w++ {
		for i := 0; i < perWorker; i++ {
			title := fmt.Sprintf("w%d-%02d", w, i)
			want[title] = "pw-" + title
		}
	}
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker+1)
	start := make(chan struct{}) // 发令枪：尽量让写与轮换同时起跑

	// 新密钥在测试 goroutine 上先备好（helper 内 t.Fatal 不可在子 goroutine 调用）
	rotateCipher := newCipherForTest(t)
	secondCipher := newCipherForTest(t)

	// 轮换 goroutine（与写并发）
	rotateErr := make(chan error, 1)
	go func() {
		<-start
		if _, err := st.RotateAllEntries(rotateCipher); err != nil {
			rotateErr <- fmt.Errorf("rotate: %w", err)
			return
		}
		rotateErr <- nil
	}()

	// 并发写 goroutine
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				title := fmt.Sprintf("w%d-%02d", w, i)
				e := model.Entry{Title: title, Category: "misc", Fields: []model.Field{
					{Key: "password", Type: "text", IsSecret: true, Value: "pw-" + title}}}
				if err := st.CreateEntry(&e); err != nil {
					errCh <- fmt.Errorf("create %s: %w", title, err)
				}
			}
		}(w)
	}

	close(start)
	wg.Wait()
	if err := <-rotateErr; err != nil {
		t.Fatal(err)
	}
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// round-trip 校验：条数一个不少 + 每条解密回登记的明文
	verify := func(stage string) {
		t.Helper()
		list, err := st.ListEntries("", "")
		if err != nil {
			t.Fatalf("%s: 列表读取失败: %v", stage, err)
		}
		if len(list) != initial+workers*perWorker {
			t.Fatalf("%s: 条数应 %d，实际 %d（坏行被静默跳过？轮换/并发写竞态）",
				stage, initial+workers*perWorker, len(list))
		}
		for _, e := range list {
			f := e.FieldByKey("password")
			if f == nil || f.Value == "" {
				t.Fatalf("%s: 条目 %q 解密失败（旧钥密文残留？）: %+v", stage, e.Title, e)
			}
			if f.Value != want[e.Title] {
				t.Fatalf("%s: 条目 %q 解密出 %q，应为 %q", stage, e.Title, f.Value, want[e.Title])
			}
		}
	}
	verify("轮换后")

	// 连续第二次轮换后仍全部可解密（覆盖 back-to-back 轮换）
	if _, err := st.RotateAllEntries(secondCipher); err != nil {
		t.Fatalf("第二次轮换失败: %v", err)
	}
	verify("二次轮换后")
}
