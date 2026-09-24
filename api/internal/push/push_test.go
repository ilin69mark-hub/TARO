// Self-test шифрования пуша: encrypt → decrypt вручную (см. U21).
// Проверяет ECDH/HKDF/AES-цепочку без сети.
package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEncryptRoundtrip(t *testing.T) {
	// клиентский ключ
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientPub := priv.PublicKey().Bytes() // 65 uncompressed
	// auth-секрет 16 байт
	auth := make([]byte, 16)
	if _, err := rand.Reader.Read(auth); err != nil {
		t.Fatal(err)
	}
	msg := []byte("вечерняя карта дня")
	pub, salt, body, err := encryptPayload(clientPub, auth, msg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(pub) != 65 || len(salt) != 16 || len(body) == 0 {
		t.Fatalf("bad shapes: %d %d %d", len(pub), len(salt), len(body))
	}
	// расшифровка стороной клиента
	ephPub, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := priv.ECDH(ephPub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), clientPub...), pub...)
	prk := hkdfExtract(auth, shared)
	ikm, _ := hkdfExpand(prk, info, 32)
	prk2 := hkdfExtract(salt, ikm)
	cek, _ := hkdfExpand(prk2, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce, _ := hkdfExpand(prk2, []byte("Content-Encoding: nonce\x00"), 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain[0] != 0x02 || !bytes.Contains(plain, msg) {
		t.Fatalf("bad plaintext: %q", plain)
	}
}

func TestVapidJWT(t *testing.T) {
	t.Setenv("VAPID_PRIVATE_KEY", "11FvQdiQe2RUimQe1i3NtUZ4l3P9nzep71YYI96e5Oo")
	priv, err := vapidKey()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := vapidJWT(priv, "https://push.example")
	if err != nil {
		t.Fatal(err)
	}
	parts := 0
	for _, c := range tok {
		if c == '.' {
			parts++
		}
	}
	if parts != 2 {
		t.Fatalf("bad jwt: %s", tok)
	}
}

func TestSendToLocalServer(t *testing.T) {
	// клиентский ключ
	cli, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := b64enc(cli.PublicKey().Bytes())
	authB := make([]byte, 16)
	if _, err := rand.Read(authB); err != nil {
		t.Fatal(err)
	}
	var gotAuth, gotEnc string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEnc = r.Header.Get("Content-Encoding")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	defer srv.Close()

	s := &Service{http: &http.Client{}}
	t.Setenv("VAPID_PRIVATE_KEY", "11FvQdiQe2RUimQe1i3NtUZ4l3P9nzep71YYI96e5Oo")
	// doSend: крипто-путь без endpoint-политики (политика — в send(),loopback там запрещён)
	st, err := s.doSend(t.Context(), Subscription{
		Endpoint: srv.URL,
		P256DH:   pubB64,
		Auth:     b64enc(authB),
	}, []byte("привет"))
	if err != nil || st != 201 {
		t.Fatalf("send: st=%d err=%v", st, err)
	}
	if !bytes.HasPrefix([]byte(gotAuth), []byte("vapid t=")) || gotEnc != "aes128gcm" || len(gotBody) == 0 {
		t.Fatalf("headers: %q %q body=%d", gotAuth, gotEnc, len(gotBody))
	}
}

// send обязан отвергать loopback/http до сети (аудит B: rebinding-защита).
func TestSendRejectsLocal(t *testing.T) {
	s := New(nil)
	for _, ep := range []string{"http://127.0.0.1:1/x", "https://127.0.0.1/x", "http://localhost/x"} {
		if _, err := s.send(t.Context(), Subscription{Endpoint: ep}, []byte("x")); err == nil {
			t.Fatalf("send accepted %s", ep)
		}
	}
}

func b64enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
