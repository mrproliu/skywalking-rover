package ssl

import "github.com/apache/skywalking-rover/pkg/tools/btf"

func (r *Register) Envoy(execute func(envoyModule *btf.UProbeExeFile) error) {
	r.addHandler("Envoy", func() (bool, error) {
		moduleName := "/envoy"
		processModules, err := r.findModules(moduleName)
		if err != nil {
			return false, err
		}
		envoyModule := processModules[moduleName]
		if envoyModule == nil {
			return false, nil
		}
		var readSymbol, writeSymbol bool
		for _, sym := range envoyModule.Symbols {
			if sym.Name == "SSL_read" {
				readSymbol = true
			} else if sym.Name == "SSL_write" {
				writeSymbol = true
			}
		}
		if !readSymbol || !writeSymbol {
			log.Debugf("found the envoy process, but the ssl read or write symbol not exists, so ignore. read: %t, write: %t",
				readSymbol, writeSymbol)
			return false, nil
		}

		if e := execute(r.linker.OpenUProbeExeFile(envoyModule.Path)); e != nil {
			return false, e
		}
		return true, nil
	})
}
