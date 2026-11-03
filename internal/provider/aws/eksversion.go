package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/chanupen/devops-lab/internal/compat"
)

// ClusterVersion is the running cluster's Kubernetes version, or "" when
// there is no cluster.
func ClusterVersion(ctx context.Context, cfg aws.Config, name string) (string, error) {
	out, err := eks.NewFromConfig(cfg).DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
	var nf *ekstypes.ResourceNotFoundException
	switch {
	case errors.As(err, &nf):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("describe EKS cluster %s: %w", name, err)
	}
	return aws.ToString(out.Cluster.Version), nil
}

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
func NewestVersion(vs []string) string { return compat.Newest(vs) }

func versionLess(a, b string) bool { return compat.Less(a, b) }
