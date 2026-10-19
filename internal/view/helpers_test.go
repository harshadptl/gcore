package view

import "testing"

func TestSplitRTMP(t *testing.T) {
	srv, key := splitRTMP("rtmp://h/in/123?tok")
	if srv != "rtmp://h/in" || key != "123?tok" {
		t.Fatalf("got %q, %q", srv, key)
	}
}
