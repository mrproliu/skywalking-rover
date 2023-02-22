package ssl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/apache/skywalking-rover/pkg/tools/btf"
	"github.com/apache/skywalking-rover/pkg/tools/elf"
	"github.com/apache/skywalking-rover/pkg/tools/host"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
	"github.com/apache/skywalking-rover/pkg/tools/version"
	"regexp"
)

var (
	goVersionRegex = regexp.MustCompile(`^go(?P<Major>\d)\.(?P<Minor>\d+)`)
)

func (r *Register) GoTLS(execute func(exeFile *btf.UProbeExeFile, elfFile *elf.File, v *version.Version) error) {
	r.addHandler("goTLS", func() (bool, error) {
		buildVersionSymbol := r.searchSymbolInModules(r.modules, func(a, b string) bool {
			return a == b
		}, "runtime.buildVersion")
		if buildVersionSymbol == nil {
			return false, nil
		}
		pidExeFile := host.GetFileInHost(fmt.Sprintf("/proc/%d/exe", r.pid))
		elfFile, err := elf.NewFile(pidExeFile)
		if err != nil {
			return false, fmt.Errorf("read executable file error: %v", err)
		}
		defer elfFile.Close()

		v, err := r.getGoVersion(elfFile, buildVersionSymbol)
		if err != nil {
			return false, err
		}

		exeFile := r.linker.OpenUProbeExeFile(pidExeFile)
		if e := execute(exeFile, elfFile, v); e != nil {
			return false, e
		}
		return true, nil
	})
}

func (r *Register) getGoVersion(elfFile *elf.File, versionSymbol *profiling.Symbol) (*version.Version, error) {
	buffer, err := elfFile.ReadSymbolData(".data", versionSymbol.Location, versionSymbol.Size)
	if err != nil {
		return nil, fmt.Errorf("reading go version struct info failure: %v", err)
	}
	var t = goStringInC{}
	buf := bytes.NewReader(buffer)
	err = binary.Read(buf, binary.LittleEndian, &t)
	if err != nil {
		return nil, fmt.Errorf("read the go structure failure: %v", err)
	}
	buffer, err = elfFile.ReadSymbolData(".data", t.Ptr, t.Size)
	if err != nil {
		return nil, fmt.Errorf("read the go version failure: %v", err)
	}

	// parse versions
	submatch := goVersionRegex.FindStringSubmatch(string(buffer))
	if len(submatch) != 3 {
		return nil, fmt.Errorf("the go version is failure to identify, version: %s", string(buffer))
	}
	return version.Read(submatch[1], submatch[2], "")
}

type goStringInC struct {
	Ptr  uint64
	Size uint64
}
