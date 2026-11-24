package section

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func dsRun(t *testing.T, code int, body string) (brief.Result, *string) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/user/balance" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg := deepseekDefault()
	cfg.BaseURL = srv.URL
	env := b11Env(t, map[string]string{"deepseek_key": "sk-secret"}, brief.Sources{})
	return b11Section(t, "deepseek").Collect(t.Context(), env, cfg), &auth
}

func bal(avail string, infos ...string) string {
	return `{"is_available":` + avail + `,"balance_infos":[` + strings.Join(infos, ",") + `]}`
}

func info(cur, total string) string {
	return `{"currency":"` + cur + `","total_balance":"` + total + `","granted_balance":"0.00","topped_up_balance":"` + total + `"}`
}

func TestDeepSeekLowBalance(t *testing.T) {
	r, auth := dsRun(t, 200, bal("true", info("CNY", "4.20")))
	if r.Status != brief.StatusReport {
		t.Fatalf("4.20 status %s", r.Status)
	}
	if *auth != "Bearer sk-secret" {
		t.Fatalf("auth %q", *auth)
	}
	if v, _ := factVal(r.Facts, "balance CNY"); v != "4.20" {
		t.Fatalf("balance %q", v)
	}
	if _, ok := factVal(r.Facts, "warning"); !ok {
		t.Fatal("no warning fact")
	}
	r2, _ := dsRun(t, 200, bal("true", info("CNY", "12.00")))
	if r2.Status != brief.StatusOK {
		t.Fatalf("12.00 status %s", r2.Status)
	}
	if v, _ := factVal(r2.Facts, "balance CNY"); v != "12.00" {
		t.Fatalf("balance %q", v)
	}
}

func TestDeepSeekUnavailable(t *testing.T) {
	r, _ := dsRun(t, 200, bal("false", info("CNY", "50.00")))
	if r.Status != brief.StatusReport {
		t.Fatalf("status %s", r.Status)
	}
	if v, ok := factVal(r.Facts, "warning"); !ok || !strings.Contains(v, "unavailable") {
		t.Fatalf("warning %q %v", v, ok)
	}
}

func TestDeepSeekTwoCurrencies(t *testing.T) {
	r, _ := dsRun(t, 200, bal("true", info("CNY", "50.00"), info("USD", "3.10")))
	if r.Status != brief.StatusReport {
		t.Fatalf("status %s (USD 3.10 is below 5)", r.Status)
	}
	if v, _ := factVal(r.Facts, "balance USD"); v != "3.10" {
		t.Fatalf("USD %q", v)
	}
	if v, _ := factVal(r.Facts, "balance CNY"); v != "50.00" {
		t.Fatalf("CNY %q", v)
	}
}

func TestDeepSeekBadKeyIsError(t *testing.T) {
	r, _ := dsRun(t, 401, `{"error":{"message":"Authentication Fails"}}`)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "401") {
		t.Fatalf("got %+v", r)
	}
	if strings.Contains(r.Err, "sk-secret") {
		t.Fatal("key in error")
	}
}

func TestDeepSeekUnparseableBalanceIsError(t *testing.T) {
	r, _ := dsRun(t, 200, bal("true", info("CNY", "n/a")))
	if r.Status != brief.StatusError {
		t.Fatalf("status %s", r.Status)
	}
}
