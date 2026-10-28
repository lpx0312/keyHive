package db

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lpx0312/keyHive/internal/model"
)

// nowUTC：RFC3339 可解析、UTC（Z 后缀）、与当前时间接近
func TestNowUTC(t *testing.T) {
	s := nowUTC()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("nowUTC 应为 RFC3339: %q %v", s, err)
	}
	if !strings.HasSuffix(s, "Z") {
		t.Fatalf("nowUTC 应为 UTC（Z 后缀）: %q", s)
	}
	if diff := time.Since(ts); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("nowUTC 偏差过大: %q (%v)", s, diff)
	}
}

// marshalFields：空切片 → []；特殊字符字段 → 合法 JSON 且可还原
func TestMarshalFieldsRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		in    []model.Field
		want  string
		check func(t *testing.T, s string)
	}{
		{"空切片", []model.Field{}, "[]", nil},
		{"特殊字符往返", []model.Field{
			{Key: `路径\下"引号"`, Description: "描述<>&", Type: model.FieldTypeURL, IsSecret: true, Value: "v"},
			{Key: "中文键", Type: model.FieldTypeMultiline},
		}, "", func(t *testing.T, s string) {
			if !json.Valid([]byte(s)) {
				t.Fatalf("应产出合法 JSON: %s", s)
			}
			var back []model.Field
			if err := json.Unmarshal([]byte(s), &back); err != nil {
				t.Fatalf("应可 Unmarshal: %v", err)
			}
			if len(back) != 2 || back[0].Key != `路径\下"引号"` || back[0].Value != "v" || !back[0].IsSecret {
				t.Fatalf("往返结果不一致: %+v", back)
			}
			if back[1].Value != "" {
				t.Fatalf("空 value 应丢失（omitempty）: %+v", back[1])
			}
		}},
	}
	for _, c := range cases {
		s, err := marshalFields(c.in)
		if err != nil {
			t.Fatalf("%s: 序列化失败: %v", c.name, err)
		}
		if c.want != "" && s != c.want {
			t.Fatalf("%s: 应为 %s，实际 %s", c.name, c.want, s)
		}
		if c.check != nil {
			c.check(t, s)
		}
	}
}
