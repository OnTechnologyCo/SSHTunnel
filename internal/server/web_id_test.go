package server

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestWebIDCollisionDoesNotReplaceExistingTunnel(t *testing.T) {
	existing := &lease{}
	active := map[string]*lease{"000000": existing}
	// 첫 후보가 사용 중이면 다음 난수로 다른 주소를 발급합니다.
	var entropy bytes.Buffer
	binary.Write(&entropy, binary.BigEndian, uint32(0))
	binary.Write(&entropy, binary.BigEndian, uint32(1))
	id, err := newWebID(active, &entropy)
	if err != nil || id != "000001" || active["000000"] != existing {
		t.Fatalf("collision handling: id=%q err=%v", id, err)
	}
	if _, err := newWebID(active, bytes.NewReader(make([]byte, 128))); err == nil {
		t.Fatal("repeated collisions must fail without overwriting the existing tunnel")
	}
}

func TestWebIDEntropyAndFormat(t *testing.T) {
	if _, err := newWebID(nil, strings.NewReader("")); err != io.EOF {
		t.Fatalf("entropy error was not returned: %v", err)
	}
	active := make(map[string]*lease)
	for i := 0; i < 1024; i++ {
		id, err := newWebID(active, rand.Reader)
		if err != nil || !validWebID(id) {
			t.Fatalf("invalid ID: %q %v", id, err)
		}
		active[id] = &lease{}
	}
	if len(active) != 1024 {
		t.Fatal("address collision overwrote an existing tunnel")
	}
	for _, id := range []string{"", "abc12", "abc1234", "abc.12", "ABC123", "abc-12", strings.Repeat("a", 32)} {
		if validWebID(id) {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}
