package routing

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/routing/complexity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGenerations stands in for the classifier's view of the vector store.
type fakeGenerations struct {
	mu      sync.Mutex
	names   []string
	active  string
	deleted []string
	listErr error
}

func (f *fakeGenerations) ListComplexityGenerations(context.Context) ([]complexity.GenerationInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]complexity.GenerationInfo, 0, len(f.names))
	for _, name := range f.names {
		out = append(out, complexity.GenerationInfo{Namespace: name, Active: name == f.active})
	}
	return out, nil
}

func (f *fakeGenerations) DeleteComplexityGeneration(_ context.Context, namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, namespace)
	kept := f.names[:0]
	for _, name := range f.names {
		if name != namespace {
			kept = append(kept, name)
		}
	}
	f.names = kept
	return nil
}

func (f *fakeGenerations) deletedNamespaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func newTestRegistry(t *testing.T, backing *stubDimensionConfigStore, nodeID string, generations generationLister) *complexityGenerationRegistry {
	t.Helper()
	registry := newComplexityGenerationRegistry(backing, bifrost.NewDefaultLogger(schemas.LogLevelError), nodeID, generations)
	require.NotNil(t, registry)
	return registry
}

// TestSweepReclaimsUnclaimedGenerations is the behaviour that makes deletion
// automatic: a retired generation nobody is using goes away on its own.
func TestSweepReclaimsUnclaimedGenerations(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{
		names:  []string{"ns-active", "ns-retired-a", "ns-retired-b"},
		active: "ns-active",
	}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())
	assert.Empty(t, generations.deletedNamespaces(),
		"the first pass only observes; a generation must look unused twice")

	registry.sweepOnce(context.Background())
	assert.ElementsMatch(t, []string{"ns-retired-a", "ns-retired-b"}, generations.deletedNamespaces())
}

// TestSweepNeverDeletesTheServingGeneration guards the case that would break
// routing outright.
func TestSweepNeverDeletesTheServingGeneration(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Empty(t, generations.deletedNamespaces())
}

// TestSweepSparesAGenerationAnotherNodeIsServing is the property that lets this
// work without any cluster transport. A node that never learned about a
// configuration change keeps serving an older generation; its registration is
// what stops the sweeping node from pulling those vectors out from under it.
func TestSweepSparesAGenerationAnotherNodeIsServing(t *testing.T) {
	backing := newStubDimensionConfigStore()

	// A peer registered the older generation and is still heartbeating it.
	peerClaims, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-stale", "ns-old"): {NodeID: "node-stale", Namespace: "ns-old", SeenAt: time.Now().Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(peerClaims)

	generations := &fakeGenerations{names: []string{"ns-new", "ns-old", "ns-abandoned"}, active: "ns-new"}
	registry := newTestRegistry(t, backing, "node-current", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Equal(t, []string{"ns-abandoned"}, generations.deletedNamespaces(),
		"only the generation no node claims may be reclaimed")
}

// TestSweepReclaimsAfterAClaimExpires means a departed node's generation does
// not leak forever.
func TestSweepReclaimsAfterAClaimExpires(t *testing.T) {
	backing := newStubDimensionConfigStore()
	staleClaims, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-gone", "ns-old"): {NodeID: "node-gone", Namespace: "ns-old", SeenAt: time.Now().Add(-complexityGenerationExpiry - time.Minute).Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(staleClaims)

	generations := &fakeGenerations{names: []string{"ns-new", "ns-old"}, active: "ns-new"}
	registry := newTestRegistry(t, backing, "node-current", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Equal(t, []string{"ns-old"}, generations.deletedNamespaces())

	// The expired registration is pruned rather than left to accumulate.
	var claims map[string]complexityGenerationClaim
	require.NoError(t, json.Unmarshal([]byte(backing.rows[tables.ConfigComplexitySemanticGenerationsKey]), &claims))
	assert.NotContains(t, claims, complexityGenerationClaimKey("node-gone", "ns-old"))
	assert.Contains(t, claims, complexityGenerationClaimKey("node-current", "ns-new"))
}

// TestSweepDoesNothingWhenTheStoreCannotBeListed keeps an unreadable vector
// store from being mistaken for an empty one.
func TestSweepDoesNothingWhenTheStoreCannotBeListed(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-a", "ns-b"}, active: "ns-a", listErr: errConfigStoreUnavailable}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Empty(t, generations.deletedNamespaces())
}

// TestHeartbeatRecordsThisNodesGeneration covers the write every other node's
// sweep depends on.
func TestHeartbeatRecordsThisNodesGeneration(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())

	var claims map[string]complexityGenerationClaim
	require.NoError(t, json.Unmarshal([]byte(backing.rows[tables.ConfigComplexitySemanticGenerationsKey]), &claims))
	key := complexityGenerationClaimKey("node-a", "ns-active")
	require.Contains(t, claims, key)
	assert.Equal(t, "ns-active", claims[key].Namespace)
	assert.Positive(t, claims[key].SeenAt)
}

// TestRegistryNilWithoutDependencies documents deployments with no config
// store: nothing is reclaimed automatically, as before.
func TestRegistryNilWithoutDependencies(t *testing.T) {
	assert.Nil(t, newComplexityGenerationRegistry(nil, nil, "node-a", &fakeGenerations{}))
	assert.Nil(t, newComplexityGenerationRegistry(newStubDimensionConfigStore(), nil, "node-a", nil))

	var registry *complexityGenerationRegistry
	assert.NotPanics(t, func() { registry.Run(context.Background()) })
}

// TestSweepRefusesToActWhenClaimsCannotBeRead is the safety property behind the
// whole registry. An unreadable claims row means "we do not know what peers are
// serving", not "nobody is serving anything" — and only the second reading
// makes deletion safe.
func TestSweepRefusesToActWhenClaimsCannotBeRead(t *testing.T) {
	backing := newStubDimensionConfigStore()
	backing.getErr = errConfigStoreUnavailable
	generations := &fakeGenerations{names: []string{"ns-active", "ns-retired"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Empty(t, generations.deletedNamespaces(),
		"a sweep that cannot read the claims must delete nothing")
}

// TestSweepRefusesToActOnACorruptClaimsRow covers the same reasoning for a row
// that is present but undecodable.
func TestSweepRefusesToActOnACorruptClaimsRow(t *testing.T) {
	backing := newStubDimensionConfigStore()
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = "{not json"
	generations := &fakeGenerations{names: []string{"ns-active", "ns-retired"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.sweepOnce(context.Background())

	assert.Empty(t, generations.deletedNamespaces(),
		"a corrupt claims row must not be read as an empty registry")
}

// TestHeartbeatDoesNotWipePeerClaimsOnAReadFailure guards the other half: a
// heartbeat that read nothing must not write a row that drops every peer.
func TestHeartbeatDoesNotWipePeerClaimsOnAReadFailure(t *testing.T) {
	backing := newStubDimensionConfigStore()
	peer, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-peer", "ns-peer"): {NodeID: "node-peer", Namespace: "ns-peer", SeenAt: time.Now().Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(peer)
	backing.getErr = errConfigStoreUnavailable

	generations := &fakeGenerations{names: []string{"ns-active"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)
	registry.heartbeatOnce(context.Background())

	var claims map[string]complexityGenerationClaim
	require.NoError(t, json.Unmarshal([]byte(backing.rows[tables.ConfigComplexitySemanticGenerationsKey]), &claims))
	assert.Contains(t, claims, complexityGenerationClaimKey("node-peer", "ns-peer"), "a failed read must not overwrite peers out of the row")
}

// TestDeleteComplexityGenerationWithoutAClassifierReports keeps the API from
// answering "deleted" when there was nothing to delete with. Reporting success
// would tell an operator a generation was reclaimed that still exists.
func TestDeleteComplexityGenerationWithoutAClassifierReports(t *testing.T) {
	plugin := &RoutingPlugin{}
	err := plugin.DeleteComplexityGeneration(context.Background(), "BifrostComplexityRouter_abc")
	require.ErrorIs(t, err, complexity.ErrClassifierUnavailable)

	// Listing has an honest empty answer, so it stays non-fatal.
	generations, err := plugin.ListComplexityGenerations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, generations)
}

// TestSweepSparesAGenerationClaimedBetweenPasses is the window this two-pass
// rule exists for. A node activates a generation and only records its claim on
// its next heartbeat; until then the generation is live but invisible to every
// other node, and a single-pass sweep would delete it.
func TestSweepSparesAGenerationClaimedBetweenPasses(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active", "ns-just-activated"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())
	require.Empty(t, generations.deletedNamespaces(), "first pass must only observe")

	// A peer records its claim in the gap between the two passes.
	peer, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-a", "ns-active"):            {NodeID: "node-a", Namespace: "ns-active", SeenAt: time.Now().Unix()},
		complexityGenerationClaimKey("node-peer", "ns-just-activated"): {NodeID: "node-peer", Namespace: "ns-just-activated", SeenAt: time.Now().Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(peer)

	registry.sweepOnce(context.Background())
	assert.Empty(t, generations.deletedNamespaces(),
		"a claim recorded between passes must save the generation")
}

// TestSweepSkipsPersistingClaimsWhenTheLockLapses covers the lease expiring
// while deletions are in flight. The snapshot in hand was read before a peer
// could have taken the lock and heartbeated, so writing it back would erase
// that peer's claim.
func TestSweepSkipsPersistingClaimsWhenTheLockLapses(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active", "ns-retired"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())

	// A peer takes over between the passes: it holds the lock and has recorded
	// its own claim, which this node's stale snapshot does not contain.
	backing.expireLocks()
	peer, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-peer", "ns-peer"): {NodeID: "node-peer", Namespace: "ns-peer", SeenAt: time.Now().Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(peer)
	backing.stealLockDuringSweep = true

	registry.sweepOnce(context.Background())

	var claims map[string]complexityGenerationClaim
	require.NoError(t, json.Unmarshal([]byte(backing.rows[tables.ConfigComplexitySemanticGenerationsKey]), &claims))
	assert.Contains(t, claims, complexityGenerationClaimKey("node-peer", "ns-peer"),
		"a sweep that lost its lease must not write its stale snapshot back")
}

// TestManualDeleteRefusesAGenerationAPeerIsServing keeps the operator escape
// hatch from doing what the sweep is careful not to.
func TestManualDeleteRefusesAGenerationAPeerIsServing(t *testing.T) {
	backing := newStubDimensionConfigStore()
	peer, err := json.Marshal(map[string]complexityGenerationClaim{
		complexityGenerationClaimKey("node-peer", "BifrostComplexityRouter_peer"): {NodeID: "node-peer", Namespace: "BifrostComplexityRouter_peer", SeenAt: time.Now().Unix()},
	})
	require.NoError(t, err)
	backing.rows[tables.ConfigComplexitySemanticGenerationsKey] = string(peer)

	registry := newTestRegistry(t, backing, "node-a", &fakeGenerations{})

	claimed, err := registry.claimedByAnotherNode(context.Background(), "BifrostComplexityRouter_peer")
	require.NoError(t, err)
	assert.True(t, claimed, "a live peer claim must block a manual deletion")

	claimed, err = registry.claimedByAnotherNode(context.Background(), "BifrostComplexityRouter_orphan")
	require.NoError(t, err)
	assert.False(t, claimed, "an unclaimed orphan stays deletable")
}

// TestManualDeleteRefusesWhenClaimsCannotBeRead keeps an unreadable registry
// from being read as "nobody is using it".
func TestManualDeleteRefusesWhenClaimsCannotBeRead(t *testing.T) {
	backing := newStubDimensionConfigStore()
	backing.getErr = errConfigStoreUnavailable
	registry := newTestRegistry(t, backing, "node-a", &fakeGenerations{})

	claimed, err := registry.claimedByAnotherNode(context.Background(), "BifrostComplexityRouter_x")
	require.Error(t, err)
	assert.True(t, claimed, "an unknown answer must refuse, not permit")
}

// TestClaimGenerationProtectsATargetBeforeItIsActive is the whole point of
// claiming on target: a namespace under construction is claimed, so a peer's
// sweep leaves it alone even though no node is serving it yet.
func TestClaimGenerationProtectsATargetBeforeItIsActive(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active", "ns-being-built"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.ClaimGeneration(context.Background(), "ns-being-built")
	registry.heartbeatOnce(context.Background())
	registry.sweepOnce(context.Background())
	registry.sweepOnce(context.Background())

	assert.Empty(t, generations.deletedNamespaces(),
		"a namespace claimed while being built must survive both sweep passes")
}

// TestHeartbeatDoesNotDisplaceATargetClaim covers why claims are keyed by node
// and namespace together: while warming, a node is serving one generation and
// building another, and recording the first must not erase the second.
func TestHeartbeatDoesNotDisplaceATargetClaim(t *testing.T) {
	backing := newStubDimensionConfigStore()
	generations := &fakeGenerations{names: []string{"ns-active", "ns-being-built"}, active: "ns-active"}
	registry := newTestRegistry(t, backing, "node-a", generations)

	registry.ClaimGeneration(context.Background(), "ns-being-built")
	registry.heartbeatOnce(context.Background())

	var claims map[string]complexityGenerationClaim
	require.NoError(t, json.Unmarshal([]byte(backing.rows[tables.ConfigComplexitySemanticGenerationsKey]), &claims))
	assert.Contains(t, claims, complexityGenerationClaimKey("node-a", "ns-being-built"),
		"the heartbeat must not displace this node's claim on the generation it is building")
	assert.Contains(t, claims, complexityGenerationClaimKey("node-a", "ns-active"))
}
