// keyHive 原生导出 JSON 的解析（format=keyhive）：
// 与 export 端点输出对等，打通「导出备份 → 导入恢复」闭环。
package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lpx0312/keyHive/internal/model"
)

// ParseKeyhive 解析 keyHive 明文导出的条目 JSON 数组（GET /api/v1/export 的输出）。
// id/created_at/updated_at 等元数据丢弃，导入时重新生成；
// 任一敏感字段值为遮蔽占位 *** 即整体报错——遮蔽版不包含真实密钥，不允许用于恢复导入。
func ParseKeyhive(data []byte) ([]model.Entry, error) {
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}))
	if len(data) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	var list []model.Entry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("不是 keyHive 导出 JSON（应为条目数组，形如 [{title,fields,...}]）: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("文件中没有条目")
	}
	for i := range list {
		e := &list[i]
		for j := range e.Fields {
			if e.Fields[j].IsSecret && e.Fields[j].Value == model.MaskedValue {
				return nil, fmt.Errorf("条目 %q 的敏感字段 %q 值为遮蔽占位 ***：这是遮蔽导出，不包含真实密钥，不能用于恢复导入（请用明文导出的 JSON）", e.Title, e.Fields[j].Key)
			}
		}
		e.ID, e.CreatedAt, e.UpdatedAt = 0, "", ""
	}
	return list, nil
}

// KeyhiveUsername 供预览展示：从条目字段中取用户名（无则空串）
func KeyhiveUsername(e *model.Entry) string {
	if f := e.FieldByKey("username"); f != nil {
		return strings.TrimSpace(f.Value)
	}
	return ""
}
