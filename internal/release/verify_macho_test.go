package release

import (
	"debug/macho"
	"encoding/binary"
	"strings"
	"testing"
)

// machOExecutable builds a minimal 64-bit Mach-O executable with one nonempty
// __TEXT segment carrying VM_PROT_EXECUTE, so verifyExecutable reaches and
// passes every structural check apart from cpu and cpusubtype.
func machOExecutable(cpuType, cpuSubtype uint32) []byte {
	const (
		machMagic64     = 0xfeedfacf
		machTypeExec    = 2
		lcSegment64     = 0x19
		segmentCmdSize  = 72
		headerSize      = 32
		vmProtReadExec  = 5 // VM_PROT_READ|VM_PROT_EXECUTE.
		vmProtReadWrite = 7 // VM_PROT_READ|VM_PROT_WRITE|VM_PROT_EXECUTE.
	)
	out := make([]byte, 0, headerSize+segmentCmdSize)
	put32 := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	put64 := func(v uint64) { out = binary.LittleEndian.AppendUint64(out, v) }

	put32(machMagic64)
	put32(cpuType)
	put32(cpuSubtype)
	put32(machTypeExec)
	put32(1)              // ncmds.
	put32(segmentCmdSize) // sizeofcmds.
	put32(0)              // flags.
	put32(0)              // reserved.

	put32(lcSegment64)
	put32(segmentCmdSize)
	segname := make([]byte, 16)
	copy(segname, "__TEXT")
	out = append(out, segname...)
	put64(0x100000000)                 // vmaddr.
	put64(0x1000)                      // vmsize, the segment Memsz.
	put64(0)                           // fileoff.
	put64(headerSize + segmentCmdSize) // filesize, the segment Filesz.
	put32(vmProtReadWrite)             // maxprot.
	put32(vmProtReadExec)              // initprot, the segment Prot.
	put32(0)                           // nsects.
	put32(0)                           // flags.
	return out
}

// TestVerifyExecutableMasksMachOCPUSubtypeFlags is the SB23-2523 regression.
// The top byte of cpusubtype is CPU_SUBTYPE_MASK, and bun 1.4.2 sets
// CPU_SUBTYPE_LIB64 (0x80000000) on darwin-x64, so a correct x86_64 binary
// reads 0x80000003. Before the mask the two lib64 arms below were rejected with
// "Mach-O architecture is CpuAmd64, want CpuAmd64"; all four pass now.
func TestVerifyExecutableMasksMachOCPUSubtypeFlags(t *testing.T) {
	for _, test := range []struct {
		name       string
		cpuType    uint32
		cpuSubtype uint32
		arch       string
	}{
		{name: "amd64_plain", cpuType: uint32(macho.CpuAmd64), cpuSubtype: 0x00000003, arch: "amd64"},
		{name: "amd64_lib64", cpuType: uint32(macho.CpuAmd64), cpuSubtype: 0x80000003, arch: "amd64"},
		{name: "arm64_plain", cpuType: uint32(macho.CpuArm64), cpuSubtype: 0x00000000, arch: "arm64"},
		{name: "arm64_lib64", cpuType: uint32(macho.CpuArm64), cpuSubtype: 0x80000000, arch: "arm64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyExecutable(machOExecutable(test.cpuType, test.cpuSubtype), "darwin", test.arch); err != nil {
				t.Fatalf("verifyExecutable(cpusubtype 0x%08x) error = %v, want nil", test.cpuSubtype, err)
			}
		})
	}
}

// TestVerifyExecutableRejectsWrongMachOCPUSubtype keeps the subtype check real:
// masking the capability byte must not accept a genuinely different subtype,
// and the message must name cpusubtype with both the raw and masked values.
func TestVerifyExecutableRejectsWrongMachOCPUSubtype(t *testing.T) {
	for _, test := range []struct {
		name       string
		cpuType    uint32
		cpuSubtype uint32
		arch       string
	}{
		{name: "amd64_subtype_8", cpuType: uint32(macho.CpuAmd64), cpuSubtype: 0x00000008, arch: "amd64"},
		{name: "amd64_subtype_8_with_lib64", cpuType: uint32(macho.CpuAmd64), cpuSubtype: 0x80000008, arch: "amd64"},
		// arm64e is cpusubtype 2, the platform variant release artifacts must not be.
		{name: "arm64e", cpuType: uint32(macho.CpuArm64), cpuSubtype: 0x00000002, arch: "arm64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyExecutable(machOExecutable(test.cpuType, test.cpuSubtype), "darwin", test.arch)
			if err == nil {
				t.Fatalf("verifyExecutable(cpusubtype 0x%08x) = nil, want rejection", test.cpuSubtype)
			}
			if !strings.Contains(err.Error(), "Mach-O cpusubtype is") {
				t.Fatalf("error = %v, want it to name cpusubtype", err)
			}
		})
	}
}

// TestVerifyExecutableRejectsWrongMachOCPU pins the second defect fixed here:
// a cpu mismatch used to print file.Cpu for both got and want, so the message
// read "is CpuAmd64, want CpuAmd64". It must now report the cpu it saw.
func TestVerifyExecutableRejectsWrongMachOCPU(t *testing.T) {
	err := verifyExecutable(machOExecutable(uint32(macho.CpuArm64), 0), "darwin", "amd64")
	if err == nil {
		t.Fatal("verifyExecutable(arm64 binary for amd64 target) = nil, want rejection")
	}
	message := err.Error()
	if !strings.Contains(message, "Mach-O cpu is") {
		t.Fatalf("error = %v, want it to name cpu", err)
	}
	if !strings.Contains(message, macho.CpuArm64.String()) || !strings.Contains(message, macho.CpuAmd64.String()) {
		t.Fatalf("error = %v, want got %s and want %s", err, macho.CpuArm64, macho.CpuAmd64)
	}
}
