package gitops

import (
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	requestinfo "k8s.io/apiserver/pkg/endpoints/request"
)

func TestAssistProxyAuthorizationAttributes(t *testing.T) {
	factory := requestinfo.RequestInfoFactory{
		APIPrefixes: sets.NewString("api", "apis"), GrouplessAPIPrefixes: sets.NewString("api"),
	}
	for _, name := range []string{"loki:3100", "kps-prometheus:9090"} {
		t.Run(name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, "https://example/api/v1/namespaces/monitoring/services/"+name+"/proxy/api/v1/query", nil)
			if err != nil {
				t.Fatal(err)
			}
			info, err := factory.NewRequestInfo(r)
			if err != nil {
				t.Fatal(err)
			}
			if info.Verb != "get" || info.Resource != "services" || info.Subresource != "proxy" ||
				info.Namespace != "monitoring" || info.Name != name {
				t.Fatalf("authorization attributes = %+v", info)
			}
		})
	}
}
