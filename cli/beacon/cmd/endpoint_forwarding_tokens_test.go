package cmd

import (
	"os"
	"testing"
)

func forwardingTokenPipe(t *testing.T, token string) int {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(token + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return int(reader.Fd())
}

func TestResolveForwardingTokensConsumesInheritedDescriptors(t *testing.T) {
	old := endpointOpts
	t.Cleanup(func() { endpointOpts = old })

	endpointOpts.splunkHECToken = ""
	endpointOpts.splunkHECTokenFD = forwardingTokenPipe(t, "splunk-token")
	endpointOpts.falconHECToken = ""
	endpointOpts.falconHECTokenFD = forwardingTokenPipe(t, "falcon-token")

	if err := resolveForwardingTokens(); err != nil {
		t.Fatal(err)
	}
	if got, want := endpointOpts.splunkHECToken, "splunk-token"; got != want {
		t.Fatalf("Splunk token = %q, want %q", got, want)
	}
	if got, want := endpointOpts.falconHECToken, "falcon-token"; got != want {
		t.Fatalf("Falcon token = %q, want %q", got, want)
	}
	if got := splunkHECOptions(); got == nil || got.Token != "splunk-token" {
		t.Fatalf("Splunk options = %#v, want descriptor token", got)
	}
	if got := falconHECOptions(); got == nil || got.Token != "falcon-token" {
		t.Fatalf("Falcon options = %#v, want descriptor token", got)
	}
	if endpointOpts.splunkHECTokenFD != -1 || endpointOpts.falconHECTokenFD != -1 {
		t.Fatalf("consumed descriptors = %d, %d; want -1, -1", endpointOpts.splunkHECTokenFD, endpointOpts.falconHECTokenFD)
	}
}

func TestForwardingLiteralTokenRemainsSupported(t *testing.T) {
	got, err := forwardingTokenFromFD("literal-token", -1, "--token", "--token-fd")
	if err != nil {
		t.Fatal(err)
	}
	if got != "literal-token" {
		t.Fatalf("literal token = %q, want %q", got, "literal-token")
	}
}

func TestForwardingTokenDescriptorRejectsLiteralToken(t *testing.T) {
	fd := forwardingTokenPipe(t, "descriptor-token")
	if _, err := forwardingTokenFromFD("literal-token", fd, "--token", "--token-fd"); err == nil {
		t.Fatal("literal token and token descriptor were accepted together")
	}
}
