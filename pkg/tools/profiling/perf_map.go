package profiling

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type PerfMapLibrary struct {
}

func NewPerfMapLibrary() *PerfMapLibrary {
	return &PerfMapLibrary{}
}

func (p *PerfMapLibrary) IsSupport(filePath string) bool {
	matched, err := regexp.Match("\\/perf-\\d+\\.map$", []byte(filePath))
	return err == nil && matched
}

func (p *PerfMapLibrary) AnalyzeSymbols(filePath string) ([]*Symbol, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(f)
	symbols := make([]*Symbol, 0)
	for scanner.Scan() {
		info := strings.Split(scanner.Text(), " ")
		if len(info) != 3 {
			continue
		}
		loc, err := strconv.ParseUint(info[0], 16, 64)
		if err != nil {
			return nil, fmt.Errorf("error read addr: %s, %v", info[0], err)
		}
		size, err := strconv.ParseUint(info[1], 16, 64)
		if err != nil {
			return nil, fmt.Errorf("error read size: %s, %v", info[1], err)
		}
		symbols = append(symbols, &Symbol{
			Name:     info[2],
			Location: loc,
			Size:     size,
		})
	}

	sort.SliceStable(symbols, func(i, j int) bool {
		return symbols[i].Location < symbols[j].Location
	})

	return symbols, nil
}

func (p *PerfMapLibrary) ToModule(pid int32, modName, modPath string, moduleRange []*ModuleRange) (*Module, error) {
	res := &Module{}
	res.Name = modName
	res.Path = modPath
	res.Ranges = moduleRange
	res.Type = ModuleTypePerfMap

	// load all symbols
	symbols, err := p.AnalyzeSymbols(modPath)
	if err != nil {
		return nil, err
	}
	res.Symbols = symbols

	return res, nil
}
