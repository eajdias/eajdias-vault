package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func decodableImage(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
		return true
	}
	return false
}

func thumbKey(name string, width int) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:20] + "_" + strconv.Itoa(width) + ".jpg"
}

// handleThumb devolve um JPEG reduzido da midia, gerando sob demanda e cacheando.
func (s *server) handleThumb(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		http.NotFound(w, r)
		return
	}
	width, _ := strconv.Atoi(r.FormValue("w"))
	if width < 64 {
		width = 400
	}
	if width > 800 {
		width = 800
	}

	key := thumbKey(name, width)
	dst := filepath.Join(s.thumbDir, key)

	if data, err := os.ReadFile(dst); err == nil {
		writeThumb(w, data)
		return
	}

	s.tmu.Lock()
	busy := s.busy[key]
	s.busy[key] = true
	s.tmu.Unlock()

	if busy {
		// outra requisicao esta gerando a mesma thumb: aguarda e serve quando surgir
		for i := 0; i < 100; i++ {
			time.Sleep(200 * time.Millisecond)
			if data, err := os.ReadFile(dst); err == nil {
				writeThumb(w, data)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	// fila global: no máximo 2 gerações simultâneas (ffmpeg/decode pesam CPU
	// e RAM do host; requisições excedentes ficam enfileiradas no canal)
	s.sem <- struct{}{}
	err := s.buildThumb(name, dst, width)
	<-s.sem

	s.tmu.Lock()
	delete(s.busy, key)
	s.tmu.Unlock()

	if err != nil {
		log.Printf("[thumb] %s: %v", name, err)
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeThumb(w, data)
}

func writeThumb(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	// nome da midia e unico (id_md5) => conteudo da thumb nunca muda
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Write(data)
}

func (s *server) buildThumb(name, dst string, width int) error {
	_, paths := s.rescan()
	path, ok := paths[name]
	if !ok {
		return fmt.Errorf("midia nao encontrada")
	}
	tmp := dst + ".tmp"

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	mime := sniffMime(f)
	f.Close()

	// 1) formatos que a stdlib/x-image abre nativamente (jpeg/png/gif/webp/bmp)
	if decodableImage(mime) {
		if img, err := decodeImage(path); err == nil {
			out, err := os.Create(tmp)
			if err != nil {
				return err
			}
			if err := jpeg.Encode(out, scaleFit(img, width), &jpeg.Options{Quality: 82}); err != nil {
				out.Close()
				return err
			}
			out.Close()
			return os.Rename(tmp, dst)
		}
		// falhou decodificacao -> cai no ffmpeg abaixo
	}

	// 2) fallback universal: ffmpeg extrai frame de mp4/mkv/webm/avi/wmv/flv/
	//    gif animado/heic/avif/tiff/etc. (-f mjpeg: saida tmp nao tem extensao util)
	for _, ss := range []string{"1", "0"} {
		cmd := exec.Command("ffmpeg", "-nostdin", "-y", "-v", "error",
			"-ss", ss, "-i", path, "-frames:v", "1",
			"-vf", fmt.Sprintf("scale=%d:-2:force_original_aspect_ratio=decrease", width),
			"-q:v", "4", "-f", "mjpeg", tmp)
		if cmd.Run() == nil {
			if fi, err := os.Stat(tmp); err == nil && fi.Size() > 0 {
				return os.Rename(tmp, dst)
			}
		}
	}
	os.Remove(tmp)
	return fmt.Errorf("sem thumb para %s (%s)", name, mime)
}

func decodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func scaleFit(src image.Image, maxW int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW {
		return src
	}
	nh := h * maxW / w
	dst := image.NewRGBA(image.Rect(0, 0, maxW, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}
