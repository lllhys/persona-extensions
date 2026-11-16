package model

import "testing"

func TestRefused(t *testing.T) {
	for msg, want := range map[string]bool{
		"model.complete: ai: the model declined to answer":           true,
		"model.complete: upstream stop_reason refusal":               true,
		"model.complete: finish_reason content_filter":               true,
		"model.complete: dial tcp 127.0.0.1:443: connection refused": false,
		"model.complete: 529 overloaded":                             false,
		`model.complete: ai: HTTP 400: {"error":{"message":"the request was rejected as invalid","type":"invalid_request_error"}}`: true,
	} {
		if got := refused(msg); got != want {
			t.Errorf("refused(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestFromInvalidOutput(t *testing.T) {
	r, ok := fromInvalidOutput([]byte(`{"problem":"$.observations[0]: missing \"facet\"","text":"{\"observations\":[{}]}","usage":{"input_tokens":10,"output_tokens":5}}`))
	if !ok || r.Text != `{"observations":[{}]}` || r.InTokens != 10 || r.OutTokens != 5 || r.Nonconforming == "" {
		t.Fatalf("reply: %+v %v", r, ok)
	}
	if _, ok := fromInvalidOutput([]byte(`{"problem":"empty","text":""}`)); ok {
		t.Fatal("an empty reply is not taken")
	}
	if _, ok := fromInvalidOutput(nil); ok {
		t.Fatal("no data is not a reply")
	}
}
