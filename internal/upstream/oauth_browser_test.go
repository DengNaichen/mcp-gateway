package upstream

import "testing"

func TestCheckBrowserURL(t *testing.T) {
	allowed := []string{
		"https://auth.example.com/authorize?client_id=x&state=y",
		"http://127.0.0.1:8080/authorize",
		"http://localhost/authorize",
		"http://[::1]:9000/authorize",
	}
	for _, raw := range allowed {
		if err := checkBrowserURL(raw); err != nil {
			t.Errorf("checkBrowserURL(%q) = %v, want nil", raw, err)
		}
	}
	rejected := []string{
		"file:///Applications/Calculator.app",
		"javascript:alert(1)",
		"vscode://open?url=x",
		"http://auth.example.com/authorize",
		"https:///no-host",
		"not a url",
		"",
	}
	for _, raw := range rejected {
		if err := checkBrowserURL(raw); err == nil {
			t.Errorf("checkBrowserURL(%q) = nil, want error", raw)
		}
	}
}
