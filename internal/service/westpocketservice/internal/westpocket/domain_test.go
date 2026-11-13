package westpocket

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	wp "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"

	"connectrpc.com/connect"
)

func TestSplitConservesCentsAndHasStableRemainder(t *testing.T) {
	for _, total := range []int32{3, 100, 10000, math.MaxInt32} {
		m, own, recv, e := Split(total, 10, []Member{{UserID: 30}, {UserID: 10}, {UserID: 20}})
		if e != nil {
			t.Fatal(e)
		}
		sum := int64(0)
		for i, v := range m {
			sum += int64(v.ShareCents)
			if v.UserID != int64((i+1)*10) {
				t.Fatal("unstable allocation")
			}
		}
		if sum != int64(total) || int64(own)+int64(recv) != int64(total) {
			t.Fatal("lost remainder cents")
		}
	}
	m, own, recv, e := Split(100, 99, []Member{{UserID: 2}, {UserID: 1}})
	if e != nil || own != 0 || recv != 100 || m[0].ShareCents != 50 {
		t.Fatal("owner excluded split incorrect")
	}
	for _, members := range [][]Member{{{UserID: 1}}, {{UserID: 1}, {UserID: 1}}, {{UserID: 0}, {UserID: 1}}} {
		if _, _, _, e = Split(100, 1, members); e == nil {
			t.Fatal("invalid members accepted")
		}
	}
	if _, _, _, e = Split(1, 1, []Member{{UserID: 1}, {UserID: 2}}); e == nil {
		t.Fatal("zero-share split accepted")
	}
}

func TestSnapshotAuthenticatedEncryption(t *testing.T) {
	v, e := NewVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	secret := []byte("wxp://private-recipient")
	encrypted, e := v.Encrypt(secret, 123)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encrypted, secret) {
		t.Fatal("plaintext snapshot")
	}
	actual, e := v.Decrypt(encrypted, 123)
	if e != nil || !bytes.Equal(actual, secret) {
		t.Fatal("roundtrip failed")
	}
	if _, e = v.Decrypt(encrypted, 124); e == nil {
		t.Fatal("snapshot accepted for other pocket")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, e = v.Decrypt(encrypted, 123); e == nil {
		t.Fatal("tampering accepted")
	}
}

func TestRecognitionRevokedAmbiguousAndDuplicateIdentity(t *testing.T) {
	faces := []Face{
		{Candidates: []ProviderCandidate{{"a", 96}, {"b", 94}}},
		{Candidates: []ProviderCandidate{{"a", 97}}},
		{Candidates: []ProviderCandidate{{"a", 92}}},
		{Candidates: []ProviderCandidate{{"revoked", 99}}},
		{Status: "low_quality", Candidates: []ProviderCandidate{{"b", 99}}},
	}
	got := MatchFaces(faces, map[string]int64{"a": 10, "b": 20}, 85, 5)
	for i := range got {
		if got[i].SuggestedUserID != nil {
			t.Fatalf("face %d erroneously confirmed", i)
		}
	}
	if got[0].MatchStatus != "ambiguous" || got[1].MatchStatus != "ambiguous" || got[2].MatchStatus != "ambiguous" ||
		len(got[3].Candidates) != 0 ||
		got[4].MatchStatus != "low_quality" {
		t.Fatal("wrong uncertainty state")
	}
	single := MatchFaces(
		[]Face{{Candidates: []ProviderCandidate{{"a", 96}, {"b", 80}}}},
		map[string]int64{"a": 10, "b": 20},
		85,
		5,
	)
	if value(single[0].SuggestedUserID) != 10 {
		t.Fatal("clear match not suggested")
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	img.Set(1, 1, color.White)
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestImageNormalizationAndFormatLimits(t *testing.T) {
	data, w, h, e := NormalizeImage(testPNG(t))
	if e != nil || w != 200 || h != 100 {
		t.Fatalf("normalize: %d %d %v", w, h, e)
	}
	_, format, e := image.Decode(bytes.NewReader(data))
	if e != nil || format != "jpeg" {
		t.Fatal("not normalized JPEG")
	}
	for _, bad := range [][]byte{[]byte("not an image"), make([]byte, MaxUploadBytes+1)} {
		if _, _, _, e = NormalizeImage(bad); connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("invalid image accepted")
		}
	}
}

func TestCursorBoundToUserAndQuery(t *testing.T) {
	s := Service{CursorKey: []byte("testing")}
	token := s.cursor(10, 99, "owner")
	if id, e := s.parseCursor(token, 10, "owner"); e != nil || id != 99 {
		t.Fatal("cursor failed")
	}
	if _, e := s.parseCursor(token, 20, "owner"); e == nil {
		t.Fatal("cross-user cursor accepted")
	}
	if _, e := s.parseCursor(token, 10, "member"); e == nil {
		t.Fatal("cross-filter cursor accepted")
	}
	if _, e := s.parseCursor(token+"x", 10, "owner"); e == nil {
		t.Fatal("forged cursor accepted")
	}
}

func TestInternalCollectionRejectsMissingIdentity(t *testing.T) {
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "secret")
	h := Handler{}
	_, e := h.GetCollectionState(context.Background(), connect.NewRequest(&wp.GetCollectionStateRequest{PocketId: 1}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated && connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatalf("missing identity: %v", e)
	}
}
