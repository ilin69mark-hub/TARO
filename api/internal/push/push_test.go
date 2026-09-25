// Self-test шифрования пуша: encrypt → decrypt вручную (см. U21).
// Проверяет ECDH/HKDF/AES-цепочку без сети.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type pushLogStoreStub struct {
	exec func(context.Context, string, ...any) (pgconn.CommandTag, error)
	row  func(context.Context, string, ...any) pgx.Row
}

func (f pushLogStoreStub) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return f.exec(ctx, query, args...)
}

func (f pushLogStoreStub) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return f.row(ctx, query, args...)
}

type pushLogRowStub struct {
	createdAt time.Time
	missing   bool
	err       error
}

func (r pushLogRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.missing {
		return pgx.ErrNoRows
	}
	for _, target := range dest {
		if value, ok := target.(*time.Time); ok {
			*value = r.createdAt
		}
	}
	return nil
}

func claimRowStore(createdAt time.Time) pushLogStoreStub {
	return pushLogStoreStub{
		exec: func(context.Context, string, ...any) (pgconn.CommandTag, error) {
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
		row: func(context.Context, string, ...any) pgx.Row {
			return pushLogRowStub{createdAt: createdAt}
		},
	}
}

func TestLogOnceDistinguishesDependencyFailure(t *testing.T) {
	wantErr := errors.New("push log store unavailable")
	svc := &Service{logStore: pushLogStoreStub{
		exec: func(context.Context, string, ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, wantErr
		},
		row: func(context.Context, string, ...any) pgx.Row {
			return pushLogRowStub{err: wantErr}
		},
	}}
	claimed, _, err := svc.logOnce(context.Background(), "user", "evening", "sending")
	if claimed || !errors.Is(err, wantErr) {
		t.Fatalf("dependency failure: claimed=%v err=%v", claimed, err)
	}

	svc.logStore = pushLogStoreStub{
		exec: func(context.Context, string, ...any) (pgconn.CommandTag, error) {
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
		row: func(context.Context, string, ...any) pgx.Row {
			return pushLogRowStub{missing: true}
		},
	}
	claimed, _, err = svc.logOnce(context.Background(), "user", "evening", "sending")
	if claimed || err != nil {
		t.Fatalf("duplicate row: claimed=%v err=%v", claimed, err)
	}
}

func TestSetPushLogStatusPropagatesDependencyFailure(t *testing.T) {
	wantErr := errors.New("push status store unavailable")
	stub := claimRowStore(time.Time{})
	stub.exec = func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, wantErr
	}
	svc := &Service{logStore: stub}
	if err := svc.setPushLogStatus(context.Background(), "user", "evening", "sent", time.Time{}); !errors.Is(err, wantErr) {
		t.Fatalf("status failure: %v", err)
	}
}

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
	delimiter := -1
	for i := len(plain) - 1; i >= 0; i-- {
		if plain[i] != 0 {
			delimiter = i
			break
		}
	}
	if delimiter != len(msg) || plain[delimiter] != 2 || !bytes.Equal(plain[:delimiter], msg) {
		t.Fatalf("bad plaintext: %q", plain)
	}
}

func TestWebPushPadding(t *testing.T) {
	for _, size := range []int{0, 1, 15, 16, 31} {
		plaintext := make([]byte, size)
		for i := range plaintext {
			plaintext[i] = byte(i)
		}
		padded := padWebPushPayload(plaintext)
		if len(padded)%16 != 0 || padded[size] != 2 {
			t.Fatalf("size=%d padded=%x", size, padded)
		}
		for _, value := range padded[size+1:] {
			if value != 0 {
				t.Fatalf("size=%d nonzero padding: %x", size, padded)
			}
		}
	}
}

func TestPushKeyEncodings(t *testing.T) {
	publicKey := make([]byte, 65)
	publicKey[0] = 4
	authKey := make([]byte, 16)
	for i := range authKey {
		authKey[i] = byte(i)
	}
	for _, value := range []struct {
		name   string
		raw    []byte
		size   int
		prefix byte
	}{
		{name: "public", raw: publicKey, size: 65, prefix: 4},
		{name: "auth", raw: authKey, size: 16},
	} {
		raw := b64enc(value.raw)
		padded := base64.URLEncoding.EncodeToString(value.raw)
		canonical, ok := canonicalPushKey(raw, value.size, value.prefix)
		if !ok || canonical != raw {
			t.Fatalf("%s raw key: %q", value.name, canonical)
		}
		canonical, ok = canonicalPushKey(padded, value.size, value.prefix)
		if !ok || canonical != raw {
			t.Fatalf("%s padded key: %q", value.name, canonical)
		}
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
	pubB64 := base64.URLEncoding.EncodeToString(cli.PublicKey().Bytes())
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
		Auth:     base64.URLEncoding.EncodeToString(authB),
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
