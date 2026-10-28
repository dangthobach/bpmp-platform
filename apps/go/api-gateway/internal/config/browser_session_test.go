package config

import "testing"

func TestBrowserSessionValidation(t *testing.T) {
	t.Parallel()
	valid := BrowserSession{
		Enabled: true, CookieName: "__Host-bpmp_session",
		CSRFHeaderName: "X-CSRF-Token", RedisKeyPrefix: "bpmp:bff:session",
		OperationTimeoutMS: 100, MaxTokenBytes: 256, MaxRecordBytes: 8192,
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BrowserSession){
		"invalid cookie name": func(value *BrowserSession) { value.CookieName = "session cookie" },
		"invalid CSRF header": func(value *BrowserSession) { value.CSRFHeaderName = "X-CSRF\r\nInjected" },
		"missing prefix":      func(value *BrowserSession) { value.RedisKeyPrefix = "" },
		"unbounded token":     func(value *BrowserSession) { value.MaxTokenBytes = 0 },
		"oversized token":     func(value *BrowserSession) { value.MaxTokenBytes = 4_097 },
		"unbounded record":    func(value *BrowserSession) { value.MaxRecordBytes = 0 },
		"oversized record":    func(value *BrowserSession) { value.MaxRecordBytes = 65_537 },
		"unbounded timeout":   func(value *BrowserSession) { value.OperationTimeoutMS = 5_001 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := valid
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
