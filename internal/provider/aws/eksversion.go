package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// StandardVersions lists the EKS Kubernetes versions in standard support,
// as EKS reports them now. Extended support costs five times as much for
// the control plane, so the lab never runs on it.
func StandardVersions(ctx context.Context, cfg aws.Config) ([]string, error) {
	var vs []string
	p := eks.NewDescribeClusterVersionsPaginator(eks.NewFromConfig(cfg),
		&eks.DescribeClusterVersionsInput{VersionStatus: ekstypes.VersionStatusStandardSupport})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe EKS versions: %w", err)
		}
		for _, v := range page.ClusterVersions {
			if v.VersionStatus == ekstypes.VersionStatusStandardSupport {
				vs = append(vs, aws.ToString(v.ClusterVersion))
			}
		}
	}
	return vs, nil
}

// NewestVersion returns the highest version in vs ("1.37" > "1.9").
func NewestVersion(vs []string) string {
	best := ""
	for _, v := range vs {
		if best == "" || versionLess(best, v) {
			best = v
		}
	}
	return best
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// CheckVersion refuses any version but the newest one in standard support.
func CheckVersion(want string, standard []string) error {
	newest := NewestVersion(standard)
	switch {
	case newest == "":
		return fmt.Errorf("EKS reports no version in standard support")
	case want != newest:
		return fmt.Errorf("lab.yaml pins Kubernetes %s, but the newest EKS version in standard support is %s (standard: %s); "+
			"the cloud lab runs only on the newest: change k8sVersion in lab.yaml", want, newest, strings.Join(standard, ", "))
	}
	return nil
}
