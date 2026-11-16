package secrets

import (
	"sync"
	"testing"
)

func TestRuntimeOverridesAndEnvironmentFallback(t *testing.T) {
	t.Setenv("B1_TEST_SECRET", "environment")
	var s Store
	if s.Lookup("B1_TEST_SECRET") != "environment" {
		t.Fatal("missing env fallback")
	}
	s.Set("B1_TEST_SECRET", "runtime")
	if s.Expand("key=${B1_TEST_SECRET}") != "key=runtime" {
		t.Fatal("missing runtime expansion")
	}
	s.Set("B1_TEST_SECRET", "")
	if s.Lookup("B1_TEST_SECRET") != "environment" {
		t.Fatal("clear did not restore env fallback")
	}
	var nilStore *Store
	if nilStore.Lookup("B1_TEST_SECRET") != "environment" {
		t.Fatal("nil store fallback")
	}
}
func TestConcurrentAccess(t *testing.T) {
	var s Store
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				s.Set("key", "value")
				s.Expand("${key}")
				s.Set("key", "")
			}
		})
	}
	wg.Wait()
}
