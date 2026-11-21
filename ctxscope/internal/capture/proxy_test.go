package capture

import "testing"

func TestParseUsageSSE(t *testing.T) {
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2,\"cache_creation_input_tokens\":36247,\"cache_read_input_tokens\":10}}}\n\nevent: ping\ndata: {\"type\":\"ping\"}\n"
	u := parseUsage([]byte(body), true)
	if u == nil || u.Total() != 36259 {
		t.Fatalf("got %+v", u)
	}
}

func TestParseUsageJSON(t *testing.T) {
	u := parseUsage([]byte(`{"usage":{"input_tokens":5,"cache_read_input_tokens":7}}`), false)
	if u == nil || u.Total() != 12 {
		t.Fatalf("got %+v", u)
	}
}

func TestMainPicksLargestRequest(t *testing.T) {
	small := &Exchange{Request: []byte(`{"a":1}`)}
	big := &Exchange{Request: []byte(`{"tools":[1,2,3,4,5]}`)}
	got, err := Main([]*Exchange{small, big})
	if err != nil || got != big {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := Main(nil); err == nil {
		t.Fatal("no error for empty capture")
	}
}
