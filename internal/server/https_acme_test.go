package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

func TestCloudflareACMEIssuanceAndCleanup(t *testing.T) {
	for _, failDNS := range []bool{false, true} {
		t.Run(map[bool]string{false: "issue", true: "failed-DNS-cleanup"}[failDNS], func(t *testing.T) {
			s, _, _ := nativeFixture(t, "http://127.0.0.1:1", false)
			zone, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
			var mu sync.Mutex
			created, deleted, validated := false, false, false
			value := ""
			cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing Cloudflare auth")
				}
				result := any(nil)
				switch {
				case r.Method == "GET" && r.URL.Path == "/zones":
					if r.URL.Query().Get("name") != "example.test" {
						t.Error("wrong zone query")
					}
					result = []map[string]string{{"id": zone, "name": "example.test"}}
				case r.Method == "POST" && r.URL.Path == "/zones/"+zone+"/dns_records":
					var record struct{ Type, Name, Content string }
					if json.NewDecoder(r.Body).Decode(&record) != nil {
						t.Error("invalid record")
					}
					if record.Type != "TXT" || record.Name != "_acme-challenge.tunnel.example.test" {
						t.Error("unrelated DNS mutation", record)
					}
					value = record.Content
					created = true
					result = map[string]string{"id": id}
				case r.Method == "DELETE" && r.URL.Path == "/zones/"+zone+"/dns_records/"+id:
					deleted = true
					result = map[string]string{"id": id}
				default:
					t.Error("unexpected DNS API request", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 400)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
			}))
			defer cfServer.Close()
			cf := &cloudflareDNS{token: "test-token", client: cfServer.Client(), baseURL: cfServer.URL}
			caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			var certificate []byte
			var acmeURL string
			caServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString([]byte(secret())))
				order := func(status string) map[string]any {
					return map[string]any{"status": status, "identifiers": []map[string]string{{"type": "dns", "value": "*.tunnel.example.test"}}, "authorizations": []string{acmeURL + "/auth"}, "finalize": acmeURL + "/finalize", "certificate": acmeURL + "/certificate"}
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/directory":
					json.NewEncoder(w).Encode(map[string]any{"newNonce": acmeURL + "/nonce", "newAccount": acmeURL + "/account", "newOrder": acmeURL + "/new-order", "meta": map[string]string{"termsOfService": acmeURL + "/terms"}})
				case "/nonce":
					w.WriteHeader(200)
				case "/account":
					w.Header().Set("Location", acmeURL+"/account/1")
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(map[string]string{"status": "valid"})
				case "/new-order":
					w.Header().Set("Location", acmeURL+"/order")
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(order("pending"))
				case "/auth":
					status := "pending"
					if validated {
						status = "valid"
					}
					json.NewEncoder(w).Encode(map[string]any{"status": status, "identifier": map[string]string{"type": "dns", "value": "tunnel.example.test"}, "wildcard": true, "challenges": []map[string]string{{"type": "dns-01", "url": acmeURL + "/challenge", "token": "mock-acme-challenge-token", "status": status}}})
				case "/challenge":
					if !created || value == "" {
						t.Error("challenge accepted before TXT record")
					}
					validated = true
					json.NewEncoder(w).Encode(map[string]string{"type": "dns-01", "url": acmeURL + "/challenge", "token": "mock-acme-challenge-token", "status": "valid"})
				case "/order":
					w.Header().Set("Location", acmeURL+"/order")
					json.NewEncoder(w).Encode(order("ready"))
				case "/finalize":
					var jws struct{ Payload string }
					if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
						t.Error(err)
					}
					payload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
					if err != nil {
						t.Error(err)
					}
					var input struct{ CSR string }
					if err = json.Unmarshal(payload, &input); err != nil {
						t.Error(err)
					}
					der, err := base64.RawURLEncoding.DecodeString(input.CSR)
					if err != nil {
						t.Error(err)
					}
					csr, err := x509.ParseCertificateRequest(der)
					if err != nil {
						t.Error(err)
						http.Error(w, "bad CSR", 400)
						return
					}
					if csr.CheckSignature() != nil || len(csr.DNSNames) != 1 || csr.DNSNames[0] != "*.tunnel.example.test" {
						t.Error("bad certificate request")
					}
					leaf := &x509.Certificate{SerialNumber: big.NewInt(5), DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
					der, err = x509.CreateCertificate(rand.Reader, leaf, leaf, csr.PublicKey, caKey)
					if err != nil {
						t.Error(err)
					}
					certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
					w.Header().Set("Location", acmeURL+"/order")
					json.NewEncoder(w).Encode(order("valid"))
				case "/certificate":
					w.Header().Set("Content-Type", "application/pem-certificate-chain")
					w.Write(certificate)
				default:
					t.Error("unexpected ACME request", r.URL.Path)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer caServer.Close()
			acmeURL = caServer.URL
			accountKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			client := &acme.Client{Key: accountKey, DirectoryURL: acmeURL + "/directory", HTTPClient: caServer.Client()}
			cfg := tlsAutomation{Domain: s.active.Web.BaseDomain, Zone: "example.test", AcceptTerms: true}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := s.https.issueWith(ctx, cfg, cf, client, func(_ context.Context, name, want string) error {
				mu.Lock()
				defer mu.Unlock()
				if name != "_acme-challenge.tunnel.example.test" || want != value || !created {
					t.Error("TXT propagation check mismatch")
				}
				if failDNS {
					return errors.New("simulated DNS failure")
				}
				return nil
			})
			if failDNS && err == nil {
				t.Fatal("DNS failure ignored")
			}
			if !failDNS && err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !created || !deleted {
				t.Fatal("temporary DNS record not cleaned up")
			}
			if !failDNS && !s.https.ready() {
				t.Fatal("issued certificate not activated")
			}
			if failDNS && s.https.ready() {
				t.Fatal("certificate activated despite DNS failure")
			}
		})
	}
}
func TestCloudflareErrorsDoNotExposeSecrets(t *testing.T) {
	token := strings.Repeat("secret", 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 9109, "message": token}}})
	}))
	defer srv.Close()
	cf := &cloudflareDNS{token: token, client: srv.Client(), baseURL: srv.URL}
	_, err := cf.zoneID(context.Background(), "example.test")
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "9109") {
		t.Fatal("unsafe API error", err)
	}
	if validateAutomation(tlsAutomation{Domain: "a.example.test", Zone: "unrelated.test", Token: token, AcceptTerms: true}) == nil {
		t.Fatal("unrelated zone accepted")
	}
	if validateAutomation(tlsAutomation{Domain: "a.example.test", Zone: "example.test", Token: token}) == nil {
		t.Fatal("terms consent missing")
	}
}
