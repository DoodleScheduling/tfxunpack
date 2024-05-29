package parser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/doodlescheduling/tfxunpack/internal/worker"
	"github.com/go-logr/logr"
	"github.com/upbound/provider-terraform/apis/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	tfMain        = "main.tf"
	tfConfig      = "config.tf"
	tfBackendFile = "backend.tf"
)

type Parser struct {
	Out          string
	AllowFailure bool
	FailFast     bool
	Workers      int
	Decoder      runtime.Decoder
	Logger       logr.Logger
}

func (p *Parser) Run(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pool := worker.New(ctx, worker.PoolOptions{
		Workers: p.Workers,
	})

	outWriter := worker.New(ctx, worker.PoolOptions{
		Workers: 1,
	})

	objects := make(chan runtime.Object, p.Workers)
	index := make(ResourceIndex)

	outWriter.Push(worker.Task(func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case obj, ok := <-objects:
				if !ok {
					return nil
				}

				if err := index.Push(obj); err != nil {
          return err       
        }
			}
		}
	}))

	multidocReader := utilyaml.NewYAMLReader(bufio.NewReader(in))

	for {
		resourceYAML, err := multidocReader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		pool.Push(worker.Task(func(ctx context.Context) error {
			obj, _, err := p.Decoder.Decode(
				resourceYAML,
				nil,
				nil)
			if err != nil {
				return nil
			}

			objects <- obj
			return nil
		}))
	}

	p.exit(pool)
	close(objects)
	p.exit(outWriter)

	module := ""

	for ref, obj := range index {
		switch {
		case ref.Group == v1beta1.Group && ref.Kind == "ProviderConfig":
			spec, err := p.handleProvider(p.Out, obj.(*v1beta1.ProviderConfig), index)
			if err != nil {
				return err
			}

			module += spec
			module += "\n"

		case ref.Group == v1beta1.Group && ref.Kind == "Workspace":
			if err := p.handleWorkspace(p.Out, obj.(*v1beta1.Workspace), index); err != nil {
				return err
			}
		}
	}

	return os.WriteFile(filepath.Join(p.Out, tfMain), []byte(module), 0640)
}

func (p *Parser) exit(waiters ...worker.Waiter) {
	for _, w := range waiters {
		err := w.Wait()
		if err != nil && !p.AllowFailure {
			p.Logger.Error(err, "error occured")
			os.Exit(1)
		}
	}
}

/*

func (p *Parser) handleResource(obj runtime.Object, gvk *schema.GroupVersionKind, out chan runtime.Object) error {
	if gvk.Version == "v1beta1" && gvk.Group == "tf.upbound.io" && gvk.Kind == "ProviderConfig" {
		out <- obj
	}

	return nil
}
*/
/*
func (p *Parser) getHelmRepositorySecret(ctx context.Context, repository *sourcev1beta2.HelmRepository, db map[ref]*resource.Resource) (*corev1.Secret, error) {
	if repository.Spec.SecretRef == nil {
		return nil, nil
	}

	lookupRef := ref{
		GroupKind: schema.GroupKind{
			Group: v1beta1.Group,
			Kind:  "Workspace",
		},
		Name: repository.Spec.SecretRef.Name,
	}

	if secret, ok := db[lookupRef]; ok {
		raw, err := secret.AsYAML()
		if err != nil {
			return nil, err
		}

		obj, _, err := h.opts.Decoder.Decode(raw, nil, nil)
		if err != nil {
			return nil, err
		}

		return obj.(*corev1.Secret), nil
	}

	return nil, fmt.Errorf("no repository secret `%v` found for helmrepository %s/%s", lookupRef, repository.Namespace, repository.Name)
}*/

func (p *Parser) getProvider(name string, resources ResourceIndex) (*v1beta1.ProviderConfig, error) {
	ref := ref{
		GroupKind: schema.GroupKind{
			Group: v1beta1.Group,
			Kind:  "ProviderConfig",
		},
		Name: name,
	}

	if obj, ok := resources[ref]; ok {
		return obj.(*v1beta1.ProviderConfig), nil
	}

	// Make git credentials available to inline and remote sources
	/*for _, cd := range pc.Spec.Credentials {
		if cd.Filename != gitCredentialsFilename {
			continue
		}
		data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
		if err != nil {
			return nil, errors.Wrap(err, errGetCreds)
		}
		// NOTE(bobh66): Put the git credentials file in /tmp/tf/<UUID> so it doesn't get removed or overwritten
		// by the remote module source case
		gitCredDir := filepath.Clean(filepath.Join("/tmp", dir))
		if err = os.MkdirAll(gitCredDir, 0700); err != nil {
			return nil, errors.Wrap(err, errWriteGitCreds)
		}

		// NOTE(ytsarev): Make go-getter pick up .git-credentials, see /.gitconfig in the container image
		err = os.Setenv("GIT_CRED_DIR", gitCredDir)
		if err != nil {
			return nil, errors.Wrap(err, errSetGitCredDir)
		}
		p := filepath.Clean(filepath.Join(gitCredDir, filepath.Base(cd.Filename)))
		if err := os.WriteFile(p, data, 0600); err != nil {
			return nil, errors.Wrap(err, errWriteGitCreds)
		}
	}*/

	return nil, fmt.Errorf("no provider config `%s`", name)
}

func (p *Parser) handleProvider(tfDir string, pc *v1beta1.ProviderConfig, resources ResourceIndex) (string, error) {
	var vars []string
	for _, vf := range pc.Spec.Credentials {
		switch vf.Source {
		case v1.CredentialsSourceSecret:
			ref := ref{
				GroupKind: schema.GroupKind{
					Group: "",
					Kind:  "Secret",
				},
				Name:      vf.SecretRef.Name,
				Namespace: vf.SecretRef.Namespace,
			}

			if obj, ok := resources[ref]; ok {
				secret := obj.(*corev1.Secret)
				for k, v := range secret.StringData {
					vars = append(vars, fmt.Sprintf("%s = \"%s\"", k, v))
				}
			} else {
				return "", fmt.Errorf("could not find providerconfig secret: %#v", ref)
			}
		}
	}

	return fmt.Sprintf("module \"%s\" {\n  source = \"./%s\"\n%s\n}", pc.Name, pc.Name, strings.Join(vars, "\n")), nil
}

func (p *Parser) handleWorkspace(tfDir string, ws *v1beta1.Workspace, resources ResourceIndex) error {
	pc, err := p.getProvider(ws.GetProviderConfigReference().Name, resources)
	if err != nil {
		return err
	}

	dir := filepath.Join(tfDir, pc.Name)
	if err := os.MkdirAll(dir, 0700); resource.Ignore(os.IsExist, err) != nil {
		return err
	}

	switch ws.Spec.ForProvider.Source {
	case v1beta1.ModuleSourceRemote:
		return fmt.Errorf("unsupported provider source: %s", v1beta1.ModuleSourceRemote)
	case v1beta1.ModuleSourceInline:
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.tf", ws.Name)), []byte(ws.Spec.ForProvider.Module), 0600); err != nil {
			return err
		}
	}

	if len(ws.Spec.ForProvider.Entrypoint) > 0 {
		entrypoint := strings.ReplaceAll(ws.Spec.ForProvider.Entrypoint, "../", "")
		dir = filepath.Join(dir, entrypoint)
	}

	if pc.Spec.Configuration != nil {
		if err := os.WriteFile(filepath.Join(dir, tfConfig), []byte(*pc.Spec.Configuration), 0600); err != nil {
			return err
		}
	}

	if pc.Spec.BackendFile != nil {
		if err := os.WriteFile(filepath.Join(dir, tfBackendFile), []byte(*pc.Spec.BackendFile), 0600); err != nil {
			return err
		}
	}

	if pc.Spec.PluginCache == nil {
		pc.Spec.PluginCache = new(bool)
		*pc.Spec.PluginCache = true
	}

	/*envs := make([]string, len(ws.Spec.ForProvider.Env))
	for idx, env := range ws.Spec.ForProvider.Env {
		runtimeVal := env.Value
		if runtimeVal == "" {
			switch {
			case env.ConfigMapKeyReference != nil:
				cm := &corev1.ConfigMap{}
				r := env.ConfigMapKeyReference
				nn := types.NamespacedName{Namespace: r.Namespace, Name: r.Name}
				if err := c.kube.Get(ctx, nn, cm); err != nil {
					return nil, errors.Wrap(err, errVarResolution)
				}
				runtimeVal, ok = cm.Data[r.Key]
				if !ok {
					return nil, errors.Wrap(fmt.Errorf("couldn't find key %v in ConfigMap %v/%v", r.Key, r.Namespace, r.Name), errVarResolution)
				}
			case env.SecretKeyReference != nil:
				s := &corev1.Secret{}
				r := env.SecretKeyReference
				nn := types.NamespacedName{Namespace: r.Namespace, Name: r.Name}
				if err := c.kube.Get(ctx, nn, s); err != nil {
					return nil, errors.Wrap(err, errVarResolution)
				}
				secretBytes, ok := s.Data[r.Key]
				if !ok {
					return nil, errors.Wrap(fmt.Errorf("couldn't find key %v in Secret %v/%v", r.Key, r.Namespace, r.Name), errVarResolution)
				}
				runtimeVal = string(secretBytes)
			}
		}
		envs[idx] = strings.Join([]string{env.Name, runtimeVal}, "=")
	}

	tf := c.terraform(dir, *pc.Spec.PluginCache, envs...)
	if cr.Status.AtProvider.Checksum != "" {
		checksum, err := tf.GenerateChecksum(ctx)
		if err != nil {
			return nil, errors.Wrap(err, errChecksum)
		}
		if cr.Status.AtProvider.Checksum == checksum {
			l.Debug("Checksums match - skip running terraform init")
			return &external{tf: tf, kube: c.kube, logger: c.logger}, errors.Wrap(tf.Workspace(ctx, meta.GetExternalName(cr)), errWorkspace)
		}
		l.Debug("Checksums don't match so run terraform init:", "old", cr.Status.AtProvider.Checksum, "new", checksum)
	}

	o := make([]terraform.InitOption, 0, len(ws.Spec.ForProvider.InitArgs))
	if pc.Spec.BackendFile != nil {
		o = append(o, terraform.WithInitArgs([]string{"-backend-config=" + filepath.Join(dir, tfBackendFile)}))
	}
	o = append(o, terraform.WithInitArgs(ws.Spec.ForProvider.InitArgs))
	if err := tf.Init(ctx, o...); err != nil {
		return nil, errors.Wrap(err, errInit)
	}
	return &external{tf: tf, kube: c.kube}, errors.Wrap(tf.Workspace(ctx, meta.GetExternalName(cr)), errWorkspace)*/
	return nil
}
