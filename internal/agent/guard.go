package agent

import (
	"encoding/json"

	"nekocode/internal/message"
)

// repeatDetector 检测「连续 N 次完全相同的工具调用」，用于识别模型卡住（R8）。
//
// 判定是连续的：中间插入任何不同的调用都会把计数归零。这样「改完文件再读一遍」
// 这类合法重复不会被误杀，而原地打转（同一调用连续出现）会被抓住。
type repeatDetector struct {
	limit   int
	lastKey string
	count   int
	hit     bool
}

func newRepeatDetector(limit int) *repeatDetector {
	return &repeatDetector{limit: limit}
}

// feed 按顺序喂入一轮里的全部调用，返回是否已判定为卡住。
// 一旦命中就保持命中，循环随即中止。
func (d *repeatDetector) feed(calls []message.ToolCall) bool {
	for _, c := range calls {
		key := callKey(c)
		if key == d.lastKey {
			d.count++
		} else {
			d.lastKey = key
			d.count = 1
		}
		if d.count >= d.limit {
			d.hit = true
		}
	}
	return d.hit
}

// callKey 是「工具名 + 规范化参数」的比较键。
// encoding/json 序列化 map 时按 key 排序，所以参数顺序不同也算同一次调用。
func callKey(c message.ToolCall) string {
	b, err := json.Marshal(c.Input)
	if err != nil {
		return c.Name + "\x00?"
	}
	return c.Name + "\x00" + string(b)
}
