package ssl

import (
	"fmt"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
	"github.com/apache/skywalking-rover/pkg/tools/version"
	"os/exec"
	"regexp"
	"strings"
)

var (
	nodeVersionRegex = regexp.MustCompile(`^node\.js/v(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)$`)
)

func (r *Register) Node(execute func(nodeModule, libSSLModule *profiling.Module, v *version.Version, needsReAttachSSL bool) error) {
	r.addHandler("Node", func() (bool, error) {
		moduleName1, moduleName2, libsslName := "/nodejs", "/node", "libssl.so"
		processModules, err := r.findModules(moduleName1, moduleName2, libsslName)
		if err != nil {
			return false, err
		}
		nodeModule := processModules[moduleName1]
		libsslModule := processModules[libsslName]
		needsReAttachSSL := false
		if nodeModule == nil {
			nodeModule = processModules[moduleName2]
		}
		if nodeModule == nil {
			return false, nil
		}
		if libsslModule == nil {
			if r.searchSymbolInModules([]*profiling.Module{nodeModule}, func(a, b string) bool {
				return a == b
			}, "SSL_read") == nil || r.searchSymbolInModules([]*profiling.Module{nodeModule}, func(a, b string) bool {
				return a == b
			}, "SSL_write") == nil {
				return false, nil
			}
			libsslModule = nodeModule
			needsReAttachSSL = true
		}
		v, err := r.getNodeVersion(nodeModule.Path)
		if err != nil {
			return false, err
		}
		log.Debugf("read the nodejs version, pid: %d, version: %s", r.pid, v)
		if e := execute(nodeModule, libsslModule, v, needsReAttachSSL); e != nil {
			return false, err
		}
		return true, nil
	})
}

func (r *Register) getNodeVersion(p string) (*version.Version, error) {
	result, err := exec.Command("strings", p).Output()
	if err != nil {
		return nil, err
	}
	for _, d := range strings.Split(string(result), "\n") {
		versionInfo := nodeVersionRegex.FindStringSubmatch(strings.TrimSpace(d))
		if len(versionInfo) != 4 {
			continue
		}
		return version.Read(versionInfo[1], versionInfo[2], versionInfo[3])
	}

	return nil, fmt.Errorf("nodejs version is not found")
}
