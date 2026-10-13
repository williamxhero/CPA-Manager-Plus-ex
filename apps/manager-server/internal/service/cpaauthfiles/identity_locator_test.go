package cpaauthfiles

import (
	"errors"
	"testing"
)

func TestMissingAccountEvidenceRequiresMatchingCredentialLocator(t *testing.T) {
	for _, provider := range []string{"qwen", "xai", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			file := File{ID: "runtime", Name: "same.json", AuthIndex: "current", Provider: provider}
			for _, identity := range []Identity{
				{AuthFileName: "same.json", Provider: provider},
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display"},
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display", AuthIndex: " "},
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display", RuntimeID: " "},
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display", AuthIndex: "old"},
			} {
				if err := VerifyResolvedIdentity(file, identity); !errors.Is(err, ErrIdentityMismatch) {
					t.Fatalf("unproven locator accepted: %+v: %v", identity, err)
				}
			}
			for _, identity := range []Identity{
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display", AuthIndex: "current"},
				{AuthFileName: "same.json", Provider: provider, AccountSnapshot: "Display", RuntimeID: "runtime"},
			} {
				if err := VerifyResolvedIdentity(file, identity); err != nil {
					t.Fatalf("matching locator rejected: %v", err)
				}
			}
		})
	}
}
