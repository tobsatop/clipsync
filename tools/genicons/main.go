// genicons writes clipsync's app icon for the builds:
//
//	-syso DIR     Windows resource objects (rsrc_windows_<arch>.syso) that
//	              go build links into the .exe, so Explorer shows the icon
//	-iconset DIR  a macOS .iconset folder, for iconutil to turn into .icns
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"clipsync/internal/icons"
)

func main() {
	syso := flag.String("syso", "", "directory for rsrc_windows_*.syso")
	iconset := flag.String("iconset", "", "directory for the macOS iconset")
	flag.Parse()
	if *syso != "" {
		if err := writeSyso(*syso); err != nil {
			log.Fatal(err)
		}
	}
	if *iconset != "" {
		if err := writeIconset(*iconset); err != nil {
			log.Fatal(err)
		}
	}
}

func writeIconset(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	app := icons.App(0.09) // macOS icons leave a margin around the tile
	for _, s := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", s, s)
			if scale == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", s, s)
			}
			if err := os.WriteFile(filepath.Join(dir, name), icons.PNG(icons.Render(s*scale, app)), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- Windows resources -------------------------------------------------------

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langEnUS    = 0x0409
)

type resource struct {
	typ, id uint16
	data    []byte
}

func writeSyso(dir string) error {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	app := icons.App(0)
	var res []resource
	var group bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&group, le, [3]uint16{0, 1, uint16(len(sizes))}) // GRPICONDIR
	for i, s := range sizes {
		img := icons.PNG(icons.Render(s, app)) // PNG icon images: Windows Vista and later
		id := uint16(i + 1)
		res = append(res, resource{rtIcon, id, img})
		dim := byte(s)
		if s >= 256 {
			dim = 0
		}
		group.Write([]byte{dim, dim, 0, 0})        // width, height, colours, reserved
		binary.Write(&group, le, [2]uint16{1, 32}) // planes, bits per pixel
		binary.Write(&group, le, uint32(len(img))) // bytes in resource
		binary.Write(&group, le, id)               // RT_ICON id
	}
	res = append(res, resource{rtGroupIcon, 1, group.Bytes()})

	// COFF relocation types for "image-relative address", per machine.
	for _, m := range []struct {
		arch    string
		machine uint16
		reloc   uint16
	}{{"amd64", 0x8664, 0x0003}, {"arm64", 0xAA64, 0x0002}} {
		obj := coff(m.machine, m.reloc, res)
		if err := os.WriteFile(filepath.Join(dir, "rsrc_windows_"+m.arch+".syso"), obj, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// rsrcSection lays out a PE resource directory (type → id → language →
// data entry) followed by the data. It returns the section and the offsets
// of the data-entry address fields, which the linker must relocate.
func rsrcSection(res []resource) ([]byte, []uint32) {
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint16
	byType := map[uint16][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	const dirHdr, entry, dataEntry = 16, 8, 16
	off := dirHdr + entry*len(types)
	typeDir := map[uint16]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dirHdr + entry*len(byType[t])
	}
	langDir := make([]int, len(res))
	for i := range res {
		langDir[i] = off
		off += dirHdr + entry
	}
	dataEnt := make([]int, len(res))
	for i := range res {
		dataEnt[i] = off
		off += dataEntry
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		off = (off + 7) &^ 7
		dataOff[i] = off
		off += len(r.data)
	}
	buf := make([]byte, (off+7)&^7)
	le := binary.LittleEndian
	dir := func(at, ids int) { le.PutUint16(buf[at+14:], uint16(ids)) } // NumberOfIdEntries
	ent := func(at int, id uint32, target uint32) {
		le.PutUint32(buf[at:], id)
		le.PutUint32(buf[at+4:], target)
	}
	const subdir = 0x80000000
	dir(0, len(types))
	for k, t := range types {
		ent(dirHdr+entry*k, uint32(t), uint32(typeDir[t])|subdir)
		dir(typeDir[t], len(byType[t]))
		for j, i := range byType[t] {
			ent(typeDir[t]+dirHdr+entry*j, uint32(res[i].id), uint32(langDir[i])|subdir)
		}
	}
	var relocs []uint32
	for i, r := range res {
		dir(langDir[i], 1)
		ent(langDir[i]+dirHdr, langEnUS, uint32(dataEnt[i]))
		le.PutUint32(buf[dataEnt[i]:], uint32(dataOff[i])) // made image-relative by the relocation
		le.PutUint32(buf[dataEnt[i]+4:], uint32(len(r.data)))
		relocs = append(relocs, uint32(dataEnt[i]))
		copy(buf[dataOff[i]:], r.data)
	}
	return buf, relocs
}

// coff wraps the resource section in a COFF object file with one section,
// its relocations, and the one symbol they refer to.
func coff(machine, relocType uint16, res []resource) []byte {
	sec, relocs := rsrcSection(res)
	const fileHdr, secHdr, relocSize = 20, 40, 10
	rawPtr := fileHdr + secHdr
	relocPtr := rawPtr + len(sec)
	symPtr := relocPtr + relocSize*len(relocs)

	var b bytes.Buffer
	le := binary.LittleEndian
	w := func(v any) { binary.Write(&b, le, v) }
	// IMAGE_FILE_HEADER
	w(machine)
	w(uint16(1))      // sections
	w(uint32(0))      // timestamp
	w(uint32(symPtr)) // symbol table
	w(uint32(1))      // symbols
	w(uint16(0))      // optional header size
	w(uint16(0))      // characteristics
	// IMAGE_SECTION_HEADER
	b.Write([]byte(".rsrc\x00\x00\x00"))
	w(uint32(0)) // virtual size
	w(uint32(0)) // virtual address
	w(uint32(len(sec)))
	w(uint32(rawPtr))
	w(uint32(relocPtr))
	w(uint32(0)) // line numbers
	w(uint16(len(relocs)))
	w(uint16(0))
	w(uint32(0x40000040)) // initialized data, readable
	b.Write(sec)
	for _, r := range relocs { // IMAGE_RELOCATION
		w(r)
		w(uint32(0)) // symbol 0: the section
		w(relocType)
	}
	// IMAGE_SYMBOL for the section, then an empty string table.
	b.Write([]byte(".rsrc\x00\x00\x00"))
	w(uint32(0)) // value
	w(int16(1))  // section number
	w(uint16(0)) // type
	w(uint8(3))  // storage class: static
	w(uint8(0))  // aux symbols
	w(uint32(4)) // string table size
	return b.Bytes()
}
