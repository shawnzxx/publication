// user-service/provider_test.go

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pact-foundation/pact-go/v2/provider"
)

func TestProvider_Verification(t *testing.T) {
	go main()
	time.Sleep(1 * time.Second)

	verifier := provider.NewVerifier()

	err := verifier.VerifyProvider(t, provider.VerifyRequest{
		ProviderBaseURL: "http://localhost:8090",
		PactFiles:       []string{filepath.ToSlash("../../../pkg/client/pacts/ShortlinkService-UserService.json")},
		ProviderVersion: "1.0.0",
		// 不设置任何 StateHandler，让 Pact 忽略 providerStates
	})
	if err != nil {
		t.Fatal(err)
	}
}
