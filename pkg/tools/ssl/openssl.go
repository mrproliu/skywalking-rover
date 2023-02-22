package ssl

import (
	"fmt"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
)

func (r *Register) OpenSSL(execute func(libCrypto, libSSL *profiling.Module) error) {
	r.addHandler("OpenSSL", func() (bool, error) {
		var libcryptoName, libsslName = "libcrypto.so", "libssl.so"
		var libcryptoPath, libsslPath string
		modules, err := r.findModules(libcryptoName, libsslName)
		if err != nil {
			return false, err
		}
		if len(modules) == 0 {
			return false, nil
		}
		if libcrypto := modules[libcryptoName]; libcrypto != nil {
			libcryptoPath = libcrypto.Path
		}
		if libssl := modules[libsslName]; libssl != nil {
			libsslPath = libssl.Path
		}
		if libcryptoPath == "" || libsslPath == "" {
			return false, fmt.Errorf("the OpenSSL library not complete, libcrypto: %s, libssl: %s", libcryptoPath, libsslPath)
		}

		if e := execute(modules[libcryptoName], modules[libsslName]); e != nil {
			return false, e
		}
		return true, nil
	})
}
