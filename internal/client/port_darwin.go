package client

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/remorses/tuistory/internal/process"
	"golang.org/x/sys/unix"
)

// Native ABI from Apple's bsd/sys/proc_info.h and libproc.c:
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h
// proc_info is the syscall underlying proc_pidinfo and proc_pidfdinfo.
const (
	procInfoPID         = 2
	procInfoPIDFD       = 3
	procPIDListFDs      = 1
	procPIDFDSocketInfo = 3
	procFDTypeSocket    = 2
	socketInfoTCP       = 2
	tcpStateListen      = 1
)

type procFDInfo struct {
	FD   int32
	Type uint32
}

type procInetInfo struct {
	ForeignPort, LocalPort int32
	Generation             uint64
	Flags, Flow            uint32
	Version, TTL           uint8
	Reserved               uint32
	ForeignAddr, LocalAddr [16]byte
	IPv4TOS                uint8
	IPv6                   struct {
		HopsLimit uint8
		Checksum  int32
		Interface uint16
		Hops      int16
	}
}

type procSocketFDInfo struct {
	File struct {
		OpenFlags, Status uint32
		Offset            int64
		Type              int32
		GuardFlags        uint32
	}
	Stat                   [17]uint64 // vinfo_stat: 136-byte fixed-width ABI.
	Socket, PCB            uint64
	Type, Protocol, Family int32
	Options                [8]int16
	OOBMark                uint32
	Receive, Send          [6]uint32 // sockbuf_info: five uint32 and two shorts.
	Kind                   int32
	Reserved               uint32
	// The protocol union is 528 bytes, aligned to uint64 by its members.
	TCP struct {
		Inet            procInetInfo
		State           int32
		Timers          [4]int32
		MSS             int32
		Flags, Reserved uint32
		PCB             uint64
	}
	OtherProtocol [408]byte
}

func procInfo(call, pid, flavor int, arg uintptr, buffer []byte) (int, error) {
	n, _, errno := syscall.Syscall6(unix.SYS_PROC_INFO, uintptr(call), uintptr(pid), uintptr(flavor), arg, uintptr(unsafe.Pointer(unsafe.SliceData(buffer))), uintptr(len(buffer)))
	runtime.KeepAlive(buffer)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func processFDs(pid int) ([]procFDInfo, error) {
	size, err := procInfo(procInfoPID, pid, procPIDListFDs, 0, nil)
	if err != nil || size == 0 {
		return nil, err
	}
	for {
		fds := make([]procFDInfo, size/int(unsafe.Sizeof(procFDInfo{}))+16)
		buffer := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(fds))), len(fds)*int(unsafe.Sizeof(procFDInfo{})))
		n, err := procInfo(procInfoPID, pid, procPIDListFDs, 0, buffer)
		if err != nil {
			return nil, err
		}
		if n < len(buffer) {
			return fds[:n/int(unsafe.Sizeof(procFDInfo{}))], nil
		}
		size = len(buffer) * 2
	}
}

func findPIDsListeningOnPort(port int) ([]int, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	pids, err := process.PIDs()
	if err != nil {
		return nil, err
	}
	var owners []int
	for _, pid := range pids {
		fds, err := processFDs(pid)
		if err != nil {
			continue
		} // Exited or not visible to this user.
		for _, fd := range fds {
			if fd.Type != procFDTypeSocket {
				continue
			}
			var info procSocketFDInfo
			buffer := unsafe.Slice((*byte)(unsafe.Pointer(&info)), int(unsafe.Sizeof(info)))
			n, err := procInfo(procInfoPIDFD, pid, procPIDFDSocketInfo, uintptr(fd.FD), buffer)
			if err != nil || n != len(buffer) || info.Kind != socketInfoTCP || info.TCP.State != tcpStateListen {
				continue
			}
			var raw [2]byte
			binary.NativeEndian.PutUint16(raw[:], uint16(info.TCP.Inet.LocalPort))
			if int(binary.BigEndian.Uint16(raw[:])) == port {
				owners = append(owners, pid)
				break
			}
		}
	}
	return owners, nil
}
