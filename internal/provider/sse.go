package provider

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseEvent 是一条解析后的 SSE 事件。
type sseEvent struct {
	Event string // event: 字段，缺省为空
	Data  string // 多行 data: 用 "\n" 拼接
}

// readSSE 从 r 读取 SSE 事件流，每凑齐一条事件就调用 fn（R8 共享 SSE 解析）。
// 返回 nil 表示流正常结束（EOF）；返回非 EOF 错误表示读取失败。
// fn 返回错误时立即停止并返回该错误。
//
// 处理：空行分隔事件；`: ` 开头的注释行忽略；`event:`/`data:` 字段；
// 多行 data 用 "\n" 拼接；CRLF 行尾兼容；结尾无空行也能刷出最后一条事件。
func readSSE(r io.Reader, fn func(sseEvent) error) error {
	br := bufio.NewReader(r)
	var ev sseEvent
	hasData := false

	// 发送函数，处理当前事件并重置状态。
	dispatch := func() error {
		if !hasData {
			return nil
		}
		if err := fn(ev); err != nil {
			return err
		}
		ev = sseEvent{}
		hasData = false
		return nil
	}

	for {
		line, err := br.ReadString('\n')
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return err
		}

		line = strings.TrimRight(line, "\r\n")

		switch {
		case line == "":
			// 空行 = 事件分隔符。
			if e := dispatch(); e != nil {
				return e
			}
		case strings.HasPrefix(line, ":"):
			// 注释/心跳，忽略。
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				ev.Event = value
			case "data":
				if hasData {
					ev.Data += "\n"
				}
				ev.Data += value
				hasData = true
			}
		}

		if eof {
			return dispatch()
		}
	}
}
