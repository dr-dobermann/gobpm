package errs

import "strings"

// CheckStr local helper function which checks if the str is empty string.
// If string is empty, then error returns with errMsg.
func CheckStr(str, errMsg string, errorClasses ...string) error {
	if str == "" {
		return New(
			M(errMsg),
			func(ecc []string) errOption {
				res := []string{}
				for _, ec := range ecc {
					ec := strings.TrimSpace(ec)
					if ec != "" {
						res = append(res, ec)
					}
				}

				return C(res...)
			}(errorClasses))
	}

	return nil
}
