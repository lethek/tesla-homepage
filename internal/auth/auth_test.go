package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	s := &Sessions{Secret: []byte("k")}
	got, err := s.Decode(s.Encode("u1", 3))
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "u1" || got.Version != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionRejectsTampering(t *testing.T) {
	s := &Sessions{Secret: []byte("k")}
	v := s.Encode("u1", 0)
	payload, sig, _ := strings.Cut(v, ".")
	forged := (&Sessions{Secret: []byte("k")}).Encode("u2", 0)
	forgedPayload, _, _ := strings.Cut(forged, ".")

	for name, bad := range map[string]string{
		"other payload": forgedPayload + "." + sig,
		"wrong secret":  (&Sessions{Secret: []byte("other")}).Encode("u1", 0),
		"no signature":  payload,
		"garbage":       "x.y",
		"truncated sig": payload + "." + sig[:len(sig)-2],
	} {
		if _, err := s.Decode(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSessionExpires(t *testing.T) {
	now := time.Now()
	s := &Sessions{Secret: []byte("k"), Now: func() time.Time { return now }}
	v := s.Encode("u1", 0)
	now = now.Add(SessionTTL + time.Second)
	if _, err := s.Decode(v); err == nil {
		t.Fatal("expired session accepted")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "secret") || CheckPassword(h, "wrong") {
		t.Fatal("password check wrong")
	}
}
