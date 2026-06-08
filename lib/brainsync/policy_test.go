package brainsync

import (
	"testing"

	"github.com/xianxu/nous/lib/brain"
)

// TestComputePolicy is the pure table for the commit/push/pull/keys
// decision — no git, no daemon. Mirrors the matrix in
// workshop/plans/000047-...-plan.md.
func TestComputePolicy(t *testing.T) {
	// recipients of length n (content irrelevant — only Shared() reads it).
	recips := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "FP"
		}
		return out
	}
	cases := []struct {
		name     string
		autosave string
		publish  string
		nRecip   int
		kind     RemoteKind
		want     BrainPolicy
	}{
		{
			name: "local-only (no remote) — commit only",
			kind: RemoteNone, nRecip: 1,
			want: BrainPolicy{Commit: true},
		},
		{
			name: "gcrypt default — full sync (no regression)",
			kind: RemoteGcrypt, nRecip: 1,
			want: BrainPolicy{Commit: true, Push: true, Pull: true, KeysAdmit: true},
		},
		{
			name: "plain remote, single recipient, no opt-in — commit only (mirror)",
			kind: RemotePlain, nRecip: 1,
			want: BrainPolicy{Commit: true},
		},
		{
			name: "plain remote, shared — pushes (shared-over-plain stays watched)",
			kind: RemotePlain, nRecip: 2,
			want: BrainPolicy{Commit: true, Push: true, Pull: true, KeysAdmit: true},
		},
		{
			name:    "plain remote, single recipient, publish:on — private-but-published opt-in",
			kind:    RemotePlain, nRecip: 1, publish: "on",
			want: BrainPolicy{Commit: true, Push: true, Pull: true}, // no KeysAdmit: not gcrypt/shared
		},
		{
			name:    "publish:on but no remote — nothing to sync to",
			kind:    RemoteNone, nRecip: 1, publish: "on",
			want: BrainPolicy{Commit: true},
		},
		{
			name:    "gcrypt, publish:off — pull keeps running, push paused",
			kind:    RemoteGcrypt, nRecip: 1, publish: "off",
			want: BrainPolicy{Commit: true, Push: false, Pull: true, KeysAdmit: true},
		},
		{
			name:    "plain shared, publish:off — push paused, pull on",
			kind:    RemotePlain, nRecip: 2, publish: "off",
			want: BrainPolicy{Commit: true, Push: false, Pull: true, KeysAdmit: true},
		},
		{
			name:     "autosave:off + gcrypt — push/pull on, no local commit",
			autosave: "off", kind: RemoteGcrypt, nRecip: 1,
			want: BrainPolicy{Commit: false, Push: true, Pull: true, KeysAdmit: true},
		},
		{
			name:     "fully opted out (autosave:off, plain mirror) — inactive",
			autosave: "off", kind: RemotePlain, nRecip: 1,
			want: BrainPolicy{}, // Active() == false
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := brain.Manifest{
				Autosave:   tc.autosave,
				Publish:    tc.publish,
				Recipients: recips(tc.nRecip),
			}
			got := ComputePolicy(m, tc.kind)
			if got != tc.want {
				t.Errorf("ComputePolicy = %+v, want %+v", got, tc.want)
			}
			if got.Active() != (tc.want.Commit || tc.want.Push || tc.want.Pull) {
				t.Errorf("Active() = %v, inconsistent with %+v", got.Active(), got)
			}
		})
	}
}
