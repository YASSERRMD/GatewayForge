package integration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func newTestArgonHasher() *auth.Argon2idHasher {
	return auth.NewArgon2idHasher(auth.Argon2idParams{
		MemoryKiB:   16 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})
}

func TestFirstAdminBootstrapConcurrencyAndReplay(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hasher := newTestArgonHasher()

	// Production mode MUST refuse bootstrap.
	prodSvc := auth.NewBootstrapService(pool, config.EnvProduction, hasher)
	if _, err := prodSvc.Bootstrap(ctx, auth.BootstrapInput{
		Email: "owner@prod.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "prod-org", OrgName: "Prod Org",
	}); !errors.Is(err, auth.ErrBootstrapDisabled) {
		t.Fatalf("expected ErrBootstrapDisabled in production, got: %v", err)
	}

	svc := auth.NewBootstrapService(pool, config.EnvTest, hasher)

	// Weak password MUST fail.
	if _, err := svc.Bootstrap(ctx, auth.BootstrapInput{
		Email: "owner@acme.test", DisplayName: "Owner", Password: "short", OrgSlug: "acme-init", OrgName: "Acme",
	}); !errors.Is(err, auth.ErrInvalidBootstrapInput) {
		t.Fatalf("expected ErrInvalidBootstrapInput for short password, got: %v", err)
	}

	// Race 12 concurrent bootstrap attempts: exactly ONE must win!
	const workers = 12
	var successes atomic.Int32
	var conflicts atomic.Int32
	var otherErrs atomic.Int32

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := svc.Bootstrap(ctx, auth.BootstrapInput{
				Email:       "owner@acme.test",
				DisplayName: "Initial Owner",
				Password:    "CorrectHorseBatteryStaple!99",
				OrgSlug:     "acme-init",
				OrgName:     "Acme Initial",
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, auth.ErrAlreadyBootstrapped):
				conflicts.Add(1)
			default:
				t.Logf("worker %d unexpected err: %v", idx, err)
				otherErrs.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if successes.Load() != 1 || conflicts.Load() != workers-1 || otherErrs.Load() != 0 {
		t.Fatalf("race invariant violated: successes=%d conflicts=%d otherErrs=%d",
			successes.Load(), conflicts.Load(), otherErrs.Load())
	}

	// Subsequent replay MUST fail with ErrAlreadyBootstrapped.
	if _, err := svc.Bootstrap(ctx, auth.BootstrapInput{
		Email:       "attacker@acme.test",
		DisplayName: "Attacker",
		Password:    "AnotherStrongPassword!99",
		OrgSlug:     "attacker-org",
		OrgName:     "Attacker",
	}); !errors.Is(err, auth.ErrAlreadyBootstrapped) {
		t.Fatalf("expected ErrAlreadyBootstrapped on replay, got: %v", err)
	}
}
