package provider

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func collectSSE(t *testing.T, input string) ([]sseEvent, error) {
	t.Helper()
	var got []sseEvent
	err := readSSE(strings.NewReader(input), func(e sseEvent) error {
		got = append(got, e)
		return nil
	})
	return got, err
}

func TestReadSSEAnthropicStyle(t *testing.T) {
	in := "event: content_block_delta\ndata: {\"type\":\"text_delta\"}\n\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Event != "content_block_delta" {
		t.Errorf("Event = %q", got[0].Event)
	}
	if got[0].Data != `{"type":"text_delta"}` {
		t.Errorf("Data = %q", got[0].Data)
	}
}

func TestReadSSEOpenAIDone(t *testing.T) {
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: [DONE]\n\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Event != "" || got[0].Data != `{"choices":[{"delta":{"content":"Hi"}}]}` {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Data != "[DONE]" {
		t.Errorf("got[1].Data = %q, want [DONE]", got[1].Data)
	}
}

func TestReadSSEMultilineData(t *testing.T) {
	in := "data: line1\ndata: line2\n\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data != "line1\nline2" {
		t.Errorf("got = %+v", got)
	}
}

func TestReadSSECommentIgnored(t *testing.T) {
	in := ": heartbeat\ndata: x\n\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data != "x" {
		t.Errorf("got = %+v", got)
	}
}

func TestReadSSENoTrailingBlankLine(t *testing.T) {
	in := "data: x" // 结尾无换行、无空行
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data != "x" {
		t.Errorf("got = %+v", got)
	}
}

func TestReadSSECRLF(t *testing.T) {
	in := "data: x\r\n\r\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data != "x" {
		t.Errorf("got = %+v", got)
	}
}

func TestReadSSEEventWithoutDataNotDispatched(t *testing.T) {
	in := "event: ping\n\n" // 有 event 无 data，不产出事件
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (event without data should not dispatch)", len(got))
	}
}

func TestReadSSEMultipleEventsReset(t *testing.T) {
	in := "event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"
	got, err := collectSSE(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Event != "a" || got[0].Data != "1" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Event != "b" || got[1].Data != "2" {
		t.Errorf("got[1] = %+v (event field should reset between events)", got[1])
	}
}

func TestReadSSEStopsOnCallbackError(t *testing.T) {
	count := 0
	err := readSSE(strings.NewReader("data: a\n\ndata: b\n\ndata: c\n\n"), func(e sseEvent) error {
		count++
		if count == 2 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (should stop after callback error)", count)
	}
}
