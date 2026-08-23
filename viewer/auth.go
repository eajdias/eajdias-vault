package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

const cookieName = "sv_session"

func newSecret(persistPath string) []byte {
	// opcional: SESSION_SECRET em hex mantem sessoes validas entre restarts
	if hexSec := os.Getenv("SESSION_SECRET"); len(hexSec) >= 32 {
		if b, err := hex.DecodeString(hexSec); err == nil && len(b) >= 16 {
			return b
		}
	}
	// segredo persistido no volume: o login sobrevive a restarts do viewer
	// (importante no uso remoto via tailscale, onde re-logar custa mais)
	if b, err := os.ReadFile(persistPath); err == nil && len(b) >= 32 {
		return b
	}
	b := make([]byte, 32)
	rand.Read(b)
	_ = os.WriteFile(persistPath, b, 0o600)
	return b
}

func (s *server) sign(exp int64) string {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%d", exp)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *server) sessionCookie(r *http.Request) *http.Cookie {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	return &http.Cookie{
		Name:     cookieName,
		Value:    fmt.Sprintf("%d.%s", exp, s.sign(exp)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
		// atras do cloudflared o acesso e https (X-Forwarded-Proto: https);
		// direto pelo tailscale e http puro — o Secure so vale no primeiro caso
		Secure: r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil,
	}
}

func (s *server) validSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	var exp int64
	var sig string
	if _, err := fmt.Sscanf(c.Value, "%d.%s", &exp, &sig); err != nil {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(exp)))
}

func (s *server) checkPassword(pw string) bool {
	return subtle.ConstantTimeCompare([]byte(pw), []byte(s.password)) == 1
}

const loginHTML = `<!doctype html>
<html lang="pt-BR">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>sank-vault · login</title>
<style>
 body{background:#0e0f13;color:#e6e6ea;font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
 .box{background:#171922;padding:2rem 2.5rem;border-radius:14px;width:min(320px,90vw);box-shadow:0 10px 40px rgba(0,0,0,.6)}
 h1{margin:0 0 1rem;font-size:1.3rem;font-weight:600}
 h1 b{color:#7c5cff}
 input{width:100%%;box-sizing:border-box;padding:.65rem .8rem;border-radius:8px;border:1px solid #2c2f3d;background:#0e0f13;color:inherit;font-size:1rem}
 button{width:100%%;margin-top:.8rem;padding:.65rem;border:0;border-radius:8px;background:#7c5cff;color:#fff;font-size:1rem;cursor:pointer}
 button:hover{filter:brightness(1.15)}
 .erro{color:#ff6b81;font-size:.85rem;margin:.6rem 0 0;text-align:center}
</style></head>
<body><form class="box" method="post"><h1>sank<b>&middot;vault</b></h1>
<input type="password" name="password" placeholder="senha" autofocus>%s
<button>entrar</button></form></body></html>
`

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	msg := ""
	status := http.StatusOK
	if r.Method == http.MethodPost {
		ip := clientIP(r)
		if !s.loginAllowed(ip) {
			log.Printf("[sank-vault] login bloqueado (rate limit) para %s", ip)
			w.Header().Set("Retry-After", "600")
			http.Error(w, "muitas tentativas — tente novamente em alguns minutos", http.StatusTooManyRequests)
			return
		}
		if r.ParseForm() == nil && s.checkPassword(r.FormValue("password")) {
			s.loginOK(ip)
			http.SetCookie(w, s.sessionCookie(r))
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.loginFail(ip)
		log.Printf("[sank-vault] tentativa de login invalida de %s", ip)
		msg = `<p class="erro">senha incorreta</p>`
		status = http.StatusUnauthorized
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, loginHTML, msg)
}

// ---- rate limit simples de login (anti brute-force p/ exposicao publica) ----

const (
	loginMaxFails   = 5                // tentativas erradas...
	loginBlockAfter = 10 * time.Minute // ...dentro desta janela => bloqueia por ela
)

type failRec struct {
	count    int
	lastFail time.Time
}

// clientIP: atraz do cloudflared o real vem no header; local cai no RemoteAddr
func clientIP(r *http.Request) string {
	if v := r.Header.Get("Cf-Connecting-Ip"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) loginAllowed(ip string) bool {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	rec, ok := s.fails[ip]
	if !ok {
		return true
	}
	if time.Since(rec.lastFail) > loginBlockAfter {
		delete(s.fails, ip)
		return true
	}
	return rec.count < loginMaxFails
}

func (s *server) loginFail(ip string) {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	rec, ok := s.fails[ip]
	if !ok || time.Since(rec.lastFail) > loginBlockAfter {
		s.fails[ip] = &failRec{count: 1, lastFail: time.Now()}
		return
	}
	rec.count++
	rec.lastFail = time.Now()
}

func (s *server) loginOK(ip string) {
	s.fmu.Lock()
	delete(s.fails, ip)
	s.fmu.Unlock()
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}
