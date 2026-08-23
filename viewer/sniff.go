package main

import (
	"bytes"
	"io"
	"strings"
)

// sniffMime detecta o tipo real do arquivo lendo os magic bytes iniciais,
// necessario pois as midias ficam armazenadas SEM extensao.
// Rebobina o leitor (se possivel) para que chamadas seguintes leiam desde o inicio.
func sniffMime(r io.Reader) string {
	buf := make([]byte, 512)
	n, _ := io.ReadFull(r, buf)
	b := buf[:n]
	if rs, ok := r.(interface {
		Seek(int64, int) (int64, error)
	}); ok {
		rs.Seek(0, io.SeekStart)
	}

	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3}): // EBML
		// distingue WebM de Matroska (MKV) pelo DocType no cabecalho
		head := b
		if len(head) > 96 {
			head = head[:96]
		}
		if bytes.Contains(head, []byte("webm")) {
			return "video/webm"
		}
		return "video/x-matroska"
	case len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")):
		switch string(b[8:12]) {
		case "heic", "heix", "mif1":
			return "image/heic"
		case "avif":
			return "image/avif"
		default:
			return "video/mp4"
		}
	case bytes.HasPrefix(b, []byte{'I', 'I', '*', 0}), bytes.HasPrefix(b, []byte{'M', 'M', 0, '*'}):
		return "image/tiff"
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI ")):
		return "video/x-msvideo"
	case bytes.HasPrefix(b, []byte("BM")):
		return "image/bmp"
	case bytes.HasPrefix(b, []byte("FLV")):
		return "video/x-flv"
	case bytes.HasPrefix(b, []byte("OggS")):
		return "video/ogg"
	case bytes.HasPrefix(b, []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}): // ASF/WMV
		return "video/x-ms-wmv"
	default:
		return "application/octet-stream"
	}
}

func kindOf(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/") && mime != "image/heic" && mime != "image/tiff":
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	default:
		return "other"
	}
}
