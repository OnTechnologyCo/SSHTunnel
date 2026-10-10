package server

import (
	"crypto/rand"
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"
)

const webIDLength = 6
const webIDSpace = 36 * 36 * 36 * 36 * 36 * 36

// newWebID는 소문자 영문·숫자 6자리 주소를 만듭니다. 호출자는 brokerMu를 보유해야 합니다.
// 공개 주소만 짧게 만들며 접속 인증키와 터널 소유권 키는 변경하지 않습니다.
func newWebID(active map[string]*lease, entropy io.Reader) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		n, err := rand.Int(entropy, big.NewInt(webIDSpace))
		if err != nil {
			return "", err
		}
		id := strconv.FormatInt(n.Int64(), 36)
		id = strings.Repeat("0", webIDLength-len(id)) + id
		if _, exists := active[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("사용하지 않는 6자리 웹 주소를 발급하지 못했습니다. 다시 연결하세요")
}

func validWebID(id string) bool {
	if len(id) != webIDLength {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
