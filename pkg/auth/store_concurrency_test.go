package auth

import (
	"fmt"
	"sync"
	"testing"
)

func TestSetCredentialConcurrentProvidersPreserveAllUpdates(t *testing.T) {
	setTestAuthHome(t)

	const providers = 24
	var wg sync.WaitGroup
	errs := make(chan error, providers)
	for i := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider := fmt.Sprintf("provider-%d", i)
			errs <- SetCredential(provider, &AuthCredential{
				AccessToken: "token-" + provider,
				Provider:    provider,
				AuthMethod:  "oauth",
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SetCredential() error = %v", err)
		}
	}

	store, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if got := len(store.Credentials); got != providers {
		t.Fatalf("credential count = %d, want %d", got, providers)
	}
	for i := range providers {
		provider := fmt.Sprintf("provider-%d", i)
		cred := store.Credentials[provider]
		if cred == nil || cred.AccessToken != "token-"+provider {
			t.Fatalf("credential %q = %+v", provider, cred)
		}
	}
}

func TestDeleteCredentialConcurrentWithSetCredential(t *testing.T) {
	setTestAuthHome(t)
	if err := SetCredential("delete-me", &AuthCredential{AccessToken: "old", Provider: "delete-me"}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- DeleteCredential("delete-me")
	}()
	go func() {
		defer wg.Done()
		errs <- SetCredential("keep-me", &AuthCredential{AccessToken: "new", Provider: "keep-me"})
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	store, err := LoadStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.Credentials["delete-me"] != nil {
		t.Fatal("deleted credential survived concurrent update")
	}
	if store.Credentials["keep-me"] == nil {
		t.Fatal("unrelated credential was lost during concurrent delete")
	}
}
