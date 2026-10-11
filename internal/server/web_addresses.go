package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Reservations never expire. Retired IDs remain unavailable to prevent old links
// from being reassigned to another service. Limits include retired IDs.
const maxWebReservations = 65536
const maxUserWebReservations = 1024
const webAddressesFile = "web-addresses.json"

type webReservation struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	KeyHash string    `json:"keyHash,omitempty"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Retired bool      `json:"retired,omitempty"`
}
type webAddressDisk struct {
	Version   int              `json:"version"`
	Addresses []webReservation `json:"addresses"`
}
type WebAddressView struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Online  bool      `json:"online"`
	Created time.Time `json:"created"`
}

func (s *Store) loadWebAddresses() error {
	s.addresses = map[string]webReservation{}
	b, err := readPrivate(filepath.Join(s.Dir, webAddressesFile), 32<<20)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("웹 주소 예약 파일을 읽을 수 없습니다: %w", err)
	}
	var disk webAddressDisk
	if err = decode(b, &disk); err != nil {
		return fmt.Errorf("웹 주소 예약 파일이 손상되었습니다. 원본을 보존하고 백업을 복구하세요: %w", err)
	}
	if disk.Version != 1 || len(disk.Addresses) > maxWebReservations {
		return errors.New("지원하지 않는 웹 주소 예약 파일입니다")
	}
	keys := map[string]bool{}
	users := map[string]int{}
	for _, a := range disk.Addresses {
		_, duplicate := s.addresses[a.ID]
		if !validWebID(a.ID) || !userName.MatchString(a.User) || len(a.Name) > 160 || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 || a.Created.IsZero() || duplicate || (a.Retired && a.KeyHash != "") || (!a.Retired && (!validSecret(a.KeyHash) || keys[a.KeyHash])) {
			return errors.New("웹 주소 예약 파일의 값 또는 중복 항목을 확인하세요. 원본은 보존됩니다")
		}
		users[a.User]++
		if users[a.User] > maxUserWebReservations {
			return errors.New("계정별 웹 주소 예약 한도를 초과했습니다")
		}
		s.addresses[a.ID] = a
		if !a.Retired {
			keys[a.KeyHash] = true
		}
	}
	return nil
}

// Caller holds addressMu. Commit to disk before changing the in-memory map.
func (s *Store) saveWebAddresses(next map[string]webReservation) error {
	disk := webAddressDisk{Version: 1, Addresses: make([]webReservation, 0, len(next))}
	for _, a := range next {
		disk.Addresses = append(disk.Addresses, a)
	}
	sort.Slice(disk.Addresses, func(i, j int) bool { return disk.Addresses[i].ID < disk.Addresses[j].ID })
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, ".web-addresses-"+secret()+".tmp")
	defer os.Remove(tmp)
	if err = writeNew(tmp, append(b, '\n')); err != nil {
		return err
	}
	if err = PrivateFile(tmp); err != nil {
		return err
	}
	if err = atomicReplace(tmp, filepath.Join(s.Dir, webAddressesFile)); err != nil {
		return err
	}
	s.addresses = next
	return nil
}
func (s *Store) allocateWebAddress(user, hash, name string, active map[string]*lease, retire string) (string, error) {
	occupied := make(map[string]*lease, len(active)+len(s.addresses))
	next := make(map[string]webReservation, len(s.addresses)+1)
	count := 0
	for id, l := range active {
		occupied[id] = l
	}
	for id, a := range s.addresses {
		occupied[id] = nil
		next[id] = a
		if a.User == user {
			count++
		}
	}
	if len(next) >= maxWebReservations || count >= maxUserWebReservations {
		return "", errors.New("저장된 웹 주소 한도입니다 (계정별 1024개 / 서버 전체 65536개, 폐기 주소 포함)")
	}
	id, err := newWebID(occupied, rand.Reader)
	if err != nil {
		return "", err
	}
	next[id] = webReservation{ID: id, User: user, KeyHash: hash, Name: name, Created: time.Now().UTC()}
	if retire != "" {
		a := next[retire]
		a.Retired = true
		a.KeyHash = ""
		next[retire] = a
	}
	if err = s.saveWebAddresses(next); err != nil {
		return "", fmt.Errorf("웹 주소를 저장하지 못했습니다. 재연결 전에 저장 공간·권한을 확인하세요: %w", err)
	}
	return id, nil
}

// Called while brokerMu is held: reservation allocation and activation are atomic.
func (s *Store) reserveWebAddress(user, hash, name string, active map[string]*lease) (string, error) {
	s.addressMu.Lock()
	defer s.addressMu.Unlock()
	for _, a := range s.addresses {
		if !a.Retired && a.KeyHash == hash {
			if a.User != user {
				return "", errors.New("다른 계정에 예약된 터널 키입니다")
			}
			if _, exists := active[a.ID]; exists {
				return "", errors.New("이미 연결된 웹 주소입니다")
			}
			return a.ID, nil
		}
	}
	return s.allocateWebAddress(user, hash, name, active, "")
}
func (s *Server) WebAddressViews() []WebAddressView {
	s.brokerMu.Lock()
	defer s.brokerMu.Unlock()
	s.Store.addressMu.Lock()
	defer s.Store.addressMu.Unlock()
	views := []WebAddressView{}
	for _, a := range s.Store.addresses {
		if a.Retired {
			continue
		}
		l, online := s.tunnels[a.ID]
		name := a.Name
		if online {
			name = l.info.Name
		}
		views = append(views, WebAddressView{ID: a.ID, User: a.User, Name: name, URL: s.active.Web.Scheme + "://" + a.ID + "." + s.active.Web.BaseDomain, Online: online, Created: a.Created})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}
func (s *Server) rotateWebAddress(id string) (string, error) {
	s.brokerMu.Lock()
	defer s.brokerMu.Unlock()
	if _, online := s.tunnels[id]; online {
		return "", errors.New("서버 프로그램의 자동 재연결을 중지하고 터널 연결을 종료한 뒤 재발급하세요")
	}
	s.Store.addressMu.Lock()
	defer s.Store.addressMu.Unlock()
	a, ok := s.Store.addresses[id]
	if !ok || a.Retired {
		return "", errors.New("현재 예약된 주소를 찾을 수 없습니다")
	}
	return s.Store.allocateWebAddress(a.User, a.KeyHash, a.Name, s.tunnels, id)
}
