package pow

import (
	"context"
	"crypto"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "golang.org/x/crypto/blake2b" // required by crypto.BLAKE2b_512
	"golang.org/x/xerrors"
)

const (
	workers = 2
	target  = 10
)

var testWorker = New(crypto.BLAKE2b_512, workers)

func TestWorker_Work(t *testing.T) {
	nonce, err := testWorker.Mine(context.Background(), nil, target)
	require.NoError(t, err)
	difficulty, err := testWorker.LeadingZerosWithNonce(nil, nonce)
	assert.GreaterOrEqual(t, difficulty, target)
	assert.NoError(t, err)
}

func TestWorker_Validate(t *testing.T) {
	tests := []*struct {
		msg             []byte
		nonce           uint64
		expLeadingZeros int
		expErr          error
	}{
		{msg: nil, nonce: 0, expLeadingZeros: 1, expErr: nil},
		{msg: nil, nonce: 4611686018451317632, expLeadingZeros: 28, expErr: nil},
		{msg: make([]byte, 10240), nonce: 0, expLeadingZeros: 1, expErr: nil},
	}

	w := &Worker{hash: crypto.BLAKE2b_512}
	for _, tt := range tests {
		zeros, err := w.LeadingZerosWithNonce(tt.msg, tt.nonce)
		assert.Equal(t, tt.expLeadingZeros, zeros)
		assert.Equal(t, tt.expErr, err)
	}
}

func TestWorker_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var err error
	go func() {
		_, err = testWorker.Mine(ctx, nil, math.MaxInt32)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()

	assert.Eventually(t, func() bool { return xerrors.Is(err, ErrCancelled) }, time.Second, 10*time.Millisecond)
}

func BenchmarkWorker(b *testing.B) {
	var (
		buf     = make([]byte, 1024)
		done    uint32
		counter uint64
	)
	go func() {
		_, _ = testWorker.worker(buf, 0, math.MaxInt32, &done, &counter)
	}()
	b.ResetTimer()
	for atomic.LoadUint64(&counter) < uint64(b.N) {
	}
	atomic.StoreUint32(&done, 1)
}
