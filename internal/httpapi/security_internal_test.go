package httpapi

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// --- WebP: вырезание EXIF/XMP из RIFF-контейнера ---

func webPChunk(id string, payload []byte) []byte {
	h := make([]byte, 8)
	copy(h, id)
	binary.LittleEndian.PutUint32(h[4:], uint32(len(payload)))
	out := append(h, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0) // выравнивание чанка по чётному смещению
	}
	return out
}

func webPContainer(chunks ...[]byte) []byte {
	body := append([]byte("WEBP"), bytes.Join(chunks, nil)...)
	head := make([]byte, 8)
	copy(head, "RIFF")
	binary.LittleEndian.PutUint32(head[4:], uint32(len(body)))
	return append(head, body...)
}

func webPChunkIDs(data []byte) []string {
	var ids []string
	for off := 12; off+8 <= len(data); {
		ids = append(ids, string(data[off:off+4]))
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		end := off + 8 + size
		if end < len(data) && end%2 == 1 {
			end++
		}
		off = end
	}
	return ids
}

func TestStripWebPMetadataRemovesEXIFAndXMP(t *testing.T) {
	c := webPContainer(
		webPChunk("VP8 ", []byte{1, 2, 3, 4}),
		webPChunk("EXIF", []byte("MM\x00*\x00\x00\x00\x08GPSDATA")),
		webPChunk("XMP ", []byte("<x:xmpmeta/>")),
		webPChunk("VP8L", []byte{5, 6, 7}),
	)
	got := stripWebPMetadata(c)
	ids := strings.Join(webPChunkIDs(got), ",")
	if strings.Contains(ids, "EXIF") || strings.Contains(ids, "XMP") {
		t.Errorf("чанки метаданных не вырезаны: %s", ids)
	}
	if !strings.Contains(ids, "VP8 ") || !strings.Contains(ids, "VP8L") {
		t.Errorf("потеряны полезные чанки: %s", ids)
	}
	if size := binary.LittleEndian.Uint32(got[4:8]); int(size) != len(got)-8 {
		t.Errorf("размер RIFF = %d, want %d", size, len(got)-8)
	}
	if bytes.Contains(got, []byte("GPSDATA")) {
		t.Error("EXIF-данные (GPS) остались в файле")
	}
}

func TestStripWebPMetadataNoopWithoutMetadata(t *testing.T) {
	c := webPContainer(webPChunk("VP8 ", []byte{1, 2, 3, 4}), webPChunk("ANIM", []byte{9}))
	if got := stripWebPMetadata(c); !bytes.Equal(got, c) {
		t.Error("файл без EXIF/XMP изменился, хотя не должен был")
	}
}

func TestStripWebPMetadataDamagedUntouched(t *testing.T) {
	damaged := webPContainer(webPChunk("VP8 ", []byte{1, 2, 3}))
	damaged = append(damaged, 'E', 'X', 'I', 'F', 0xFF, 0xFF, 0xFF, 0xFF) // битая длина
	if got := stripWebPMetadata(damaged); !bytes.Equal(got, damaged) {
		t.Error("повреждённый контейнер изменился — должен сохраняться как есть")
	}
	if got := stripWebPMetadata([]byte("not a webp at all")); !bytes.Equal(got, []byte("not a webp at all")) {
		t.Error("не-WebP данные изменились")
	}
}

func TestStripImageMetadataRoutesWebP(t *testing.T) {
	c := webPContainer(webPChunk("EXIF", []byte("GPS")))
	if got := stripImageMetadata(c, "image/webp"); bytes.Contains(got, []byte("GPS")) {
		t.Error("stripImageMetadata не вырезал EXIF из webp")
	}
}

// --- CSV-инъекция в экспорте ---

func TestRowCSVFormulaInjection(t *testing.T) {
	var buf bytes.Buffer
	rowCSV(&buf, "=1+1", "plain", "-2+3", "+SUM(A1)", "@cmd", "\ttab", "a,b")
	s := buf.String()
	for _, cell := range []string{"'=1+1", "'-2+3", "'+SUM(A1)", "'@cmd", "'\ttab"} {
		if !strings.Contains(s, cell) {
			t.Errorf("ячейка %q не защищена от формулы (строка: %q)", cell, s)
		}
	}
	if !strings.Contains(s, "plain") || !strings.Contains(s, "\"a,b\"") {
		t.Errorf("обычные ячейки испорчены (строка: %q)", s)
	}
}

// --- Словарь слабых паролей ---

func TestPasswordStrengthError(t *testing.T) {
	for _, pwd := range []string{"12345678", "Qwerty123", "PASSWORD1", "otklik-Secret-1"} {
		if msg := passwordStrengthError(pwd, "admin"); msg == "" {
			t.Errorf("слабый пароль %q принят", pwd)
		}
	}
	if msg := passwordStrengthError("my-admin-2026!Strong", "admin"); msg == "" {
		t.Error("пароль, содержащий логин, принят")
	}
	for _, pwd := range []string{"correct horse battery", "Kv9!zTq2Lm#4pR", "щука-в-прудах-жила"} {
		if msg := passwordStrengthError(pwd, "admin"); msg != "" {
			t.Errorf("надёжный пароль %q отклонён: %s", pwd, msg)
		}
	}
}
