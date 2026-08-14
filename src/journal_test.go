package main

import "testing"

func TestParseJournalJSON(t *testing.T) {
	line := []byte(`{"__REALTIME_TIMESTAMP":"1785434347000000","PRIORITY":"3","SYSLOG_IDENTIFIER":"sshd","_PID":"1421","MESSAGE":"auth failure"}`)
	l, ok := parseJournalJSON(line)
	if !ok {
		t.Fatal("entry rejected")
	}
	if l.msg != "auth failure" || l.ident != "sshd" || l.pid != "1421" || l.prio != 3 {
		t.Errorf("parsed %+v", l)
	}
	if l.ts.Unix() != 1785434347 {
		t.Errorf("timestamp = %v", l.ts)
	}
}

func TestParseJournalBinaryMessage(t *testing.T) {
	// journalctl encodes non-UTF-8 payloads as an array of byte values.
	line := []byte(`{"MESSAGE":[104,105],"PRIORITY":"6"}`)
	l, ok := parseJournalJSON(line)
	if !ok || l.msg != "hi" {
		t.Errorf("byte-array MESSAGE not decoded: %+v", l)
	}
}

func TestParseJournalRepeatedField(t *testing.T) {
	line := []byte(`{"MESSAGE":["one","two"],"PRIORITY":"6"}`)
	l, ok := parseJournalJSON(line)
	if !ok || l.msg != "one two" {
		t.Errorf("repeated MESSAGE not decoded: %+v", l)
	}
}

func TestParseJournalNonJSONBecomesMeta(t *testing.T) {
	l, ok := parseJournalJSON([]byte("-- No entries --"))
	if !ok || !l.meta {
		t.Errorf("non-JSON output should surface as a meta line: %+v", l)
	}
}

func TestFormatLogWraps(t *testing.T) {
	m := model{logWrap: true, width: 60}
	l := logLine{msg: "0123456789012345678901234567890123456789", ident: "svc", pid: "9"}
	segs := m.formatLog(l, 30)
	if len(segs) < 2 {
		t.Errorf("long line should wrap, got %d segments", len(segs))
	}

	m.logWrap = false
	if segs := m.formatLog(l, 30); len(segs) != 1 {
		t.Errorf("wrap off should give one segment, got %d", len(segs))
	}
}
