package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"
)

// LabsComponent is the component tag of AWS resources the labs' scripts
// create (lab 4's secrets), so lab down's sweep and the reaper find them.
const LabsComponent = "labs"

// LabsEnv is what lab labs adds to the lab scripts' environment in cloud
// mode: the lab registry (ECR), the nodes' platform, the session's
// temporary keys and region (no endpoint: real AWS), and the tags every
// AWS resource a script creates must carry, as the JSON list the aws CLI's
// --tags takes. It needs a running cloud lab: the tags hold its expires-at.
func (p *Provider) LabsEnv() ([]string, error) {
	exp, err := ReadExpiry(ExpiryFile(p.opts.Home))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, errors.New("no cloud lab is running (no expires-at): lab up --mode cloud")
	case err != nil:
		return nil, err
	case !exp.After(p.now()):
		return nil, fmt.Errorf("the cloud lab expired at %s: lab extend, or lab down --mode cloud", exp.UTC().Format(time.RFC3339))
	}
	return LabsEnv(p.account, p.env, LabTags(p.opts.Owner, exp))
}

// LabsEnv builds the cloud labs' environment from an account, the session
// environment (Session.Env) and the lab tags.
func LabsEnv(account string, session map[string]string, tags map[string]string) ([]string, error) {
	t := maps.Clone(tags)
	t["component"] = LabsComponent
	type tag struct{ Key, Value string }
	list := make([]tag, 0, len(t))
	for _, k := range slices.Sorted(maps.Keys(t)) {
		list = append(list, tag{k, t[k]})
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	r := LabRegistryFor(account)
	env := []string{"LAB_REGISTRY_PUSH=" + r, "LAB_REGISTRY_PULL=" + r, "LAB_PLATFORM=" + NodePlatform,
		"LAB_AWS_TAGS=" + string(b)}
	for _, k := range slices.Sorted(maps.Keys(session)) {
		env = append(env, k+"="+session[k])
	}
	return env, nil
}
