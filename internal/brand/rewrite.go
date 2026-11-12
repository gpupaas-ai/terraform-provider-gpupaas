package brand

import (
	"regexp"
	"strings"
)

// bare matches the provider's own local name `gpupaas` as a word: not part of gpupaas-ai, gpupaas-go,
// console.gpupaas.ai, gpupaas_<kind> or GPU PaaS. Go has no lookaround, so the neighbours are captured.
var bare = regexp.MustCompile(`(^|[^-\w.])gpupaas([^-\w.]|$)`)

// officialSource matches the official registry source inside required_providers.
var officialSource = regexp.MustCompile(`gpupaas-ai/gpupaas([^-\w.]|$)`)

// RewriteExample turns an official example or generated doc into this brand's: addresses, environment
// variables, the provider block and local name, variable names and the registry source. "GPU PaaS", the SDK
// name and the platform's own hosts and API group stay.
func (b Brand) RewriteExample(s string) string {
	if b.Type == Official && b.Source == "gpupaas-ai/"+Official {
		return s
	}
	s = strings.ReplaceAll(s, strings.ToUpper(Official)+"_", b.EnvPrefix)
	s = officialSource.ReplaceAllString(s, b.Source+"${1}")
	s = strings.ReplaceAll(s, Official+"_", b.Type+"_")
	// neighbours are consumed by a match, so adjacent words need another pass
	for i := 0; i < 4 && bare.MatchString(s); i++ {
		s = bare.ReplaceAllString(s, "${1}"+b.Type+"${2}")
	}
	return s
}

// Leftovers lists the lines of a text that still carry the official identity, which a partner tree must not.
func (b Brand) Leftovers(text string) []string {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		hit := strings.Contains(line, "gpupaas_") || strings.Contains(line, "GPUPAAS_") || bare.MatchString(line)
		if b.Source != "gpupaas-ai/"+Official && strings.Contains(line, "gpupaas-ai") && !strings.Contains(b.Source, "gpupaas-ai") {
			hit = true
		}
		if hit {
			out = append(out, itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
