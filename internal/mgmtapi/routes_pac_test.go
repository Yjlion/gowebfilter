package mgmtapi

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestRenderPACQuotesProxyDirective(t *testing.T) {
	proxyHost := "proxy\"; alert(1);//\nnext\\host"
	proxyPort := 8080
	pac := renderPAC(proxyHost, proxyPort, nil, nil)

	var returnLine string
	for _, line := range strings.Split(pac, "\n") {
		if strings.Contains(line, "PROXY ") {
			returnLine = strings.TrimSpace(line)
			break
		}
	}
	if returnLine == "" {
		t.Fatalf("PAC output missing proxy return line:\n%s", pac)
	}

	const prefix = "return "
	const suffix = ";"
	if !strings.HasPrefix(returnLine, prefix) || !strings.HasSuffix(returnLine, suffix) {
		t.Fatalf("proxy return line is not a single return statement: %q", returnLine)
	}
	literal := strings.TrimSuffix(strings.TrimPrefix(returnLine, prefix), suffix)
	got, err := strconv.Unquote(literal)
	if err != nil {
		t.Fatalf("proxy return value is not a valid quoted string literal %q: %v", literal, err)
	}
	want := fmt.Sprintf("PROXY %s:%d", proxyHost, proxyPort)
	if got != want {
		t.Fatalf("proxy directive = %q, want %q", got, want)
	}

	for _, raw := range []string{
		`return "PROXY proxy"; alert(1);//`,
		"//\nnext",
	} {
		if strings.Contains(pac, raw) {
			t.Fatalf("PAC output contains raw injected JavaScript %q:\n%s", raw, pac)
		}
	}
}
