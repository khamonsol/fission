/*
Copyright 2019 The Fission Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	multierror "github.com/hashicorp/go-multierror"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sCache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/yaml"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	executorUtil "github.com/fission/fission/pkg/executor/util"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/cli"
	"github.com/fission/fission/pkg/fission-cli/cmd"
	"github.com/fission/fission/pkg/fission-cli/cmd/spec/types"
	"github.com/fission/fission/pkg/fission-cli/console"
	"github.com/fission/fission/pkg/fission-cli/util"
	"github.com/fission/fission/pkg/utils"
)

const (
	FISSION_DEPLOYMENT_NAME_KEY = "fission-name"
	FISSION_DEPLOYMENT_UID_KEY  = "fission-uid"

	SPEC_API_VERSION          = "fission.io/v1"
	ARCHIVE_URL_PREFIX string = "archive://"
	SPEC_README               = `
Fission Specs
=============

This is a set of specifications for a Fission app.  This includes functions,
environments, and triggers; we collectively call these things "resources".

How to use these specs
----------------------

These specs are handled with the 'fission spec' command.  See 'fission spec --help'.

'fission spec apply' will "apply" all resources specified in this directory to your
cluster.  That means it checks what resources exist on your cluster, what resources are
specified in the specs directory, and reconciles the difference by creating, updating or
deleting resources on the cluster.

'fission spec apply' will also package up your source code (or compiled binaries) and
upload the archives to the cluster if needed.  It uses 'ArchiveUploadSpec' resources in
this directory to figure out which files to archive.

You can use 'fission spec apply --watch' to watch for file changes and continuously keep
the cluster updated.

You can add YAMLs to this directory by writing them manually, but it's easier to generate
them.  Use 'fission function create --spec' to generate a function spec,
'fission environment create --spec' to generate an environment spec, and so on.

You can edit any of the files in this directory, except 'fission-deployment-config.yaml',
which contains a UID that you should never change.  To apply your changes simply use
'fission spec apply'.

fission-deployment-config.yaml
------------------------------

fission-deployment-config.yaml contains a UID.  This UID is what fission uses to correlate
resources on the cluster to resources in this directory.

All resources created by 'fission spec apply' are annotated with this UID.  Resources on
the cluster that are _not_ annotated with this UID are never modified or deleted by
fission.

`
)

// CLI spec types
type (
	FissionResources struct {
		DeploymentConfig        types.DeploymentConfig
		Packages                []fv1.Package
		Functions               []fv1.Function
		Environments            []fv1.Environment
		HttpTriggers            []fv1.HTTPTrigger
		KubernetesWatchTriggers []fv1.KubernetesWatchTrigger
		TimeTriggers            []fv1.TimeTrigger
		MessageQueueTriggers    []fv1.MessageQueueTrigger
		ArchiveUploadSpecs      []types.ArchiveUploadSpec

		SourceMap SourceMap
	}

	ResourceApplyStatus struct {
		Created []*metav1.ObjectMeta
		Updated []*metav1.ObjectMeta
		Deleted []*metav1.ObjectMeta
	}

	Location struct {
		Path string
		Line int
	}

	SourceMap struct {
		// kind -> namespace -> name -> location
		Locations map[string](map[string](map[string]Location))
	}
)

// save saves object encoded value to spec file under given spec directory
func save(data []byte, specDir string, specFile string, truncate bool) error {
	// verify
	if _, err := os.Stat(filepath.Join(specDir, "fission-deployment-config.yaml")); os.IsNotExist(err) {
		return fmt.Errorf("couldn't find specs, run `fission spec init` first: %w", err)
	}

	filename := filepath.Join(specDir, specFile)
	// check if the file is new
	newFile := false
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		if truncate {
			return fmt.Errorf("spec file does not exists")
		}
		newFile = true
	}

	// open spec file to append or write
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("couldn't create spec file: %w", err)
	}
	defer f.Close()

	if truncate {
		err = f.Truncate(0)
		if err != nil {
			return fmt.Errorf("couldn't truncate the spec file: %w", err)
		}

	} else {
		// if we're appending, add a yaml document separator
		if !newFile {
			_, err = f.Write([]byte("\n---\n"))
			if err != nil {
				return fmt.Errorf("couldn't write to spec file: %w", err)
			}
		}
	}

	// write our resource
	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("couldn't write to spec file: %w", err)
	}
	return nil
}

// called from `fission * create --spec`
func SpecSave(resource interface{}, specFile string, update bool) error {
	var specDir = "specs"

	meta, kind, data, err := crdToYaml(resource)
	if err != nil {
		return err
	}

	fr, err := ReadSpecs(specDir, util.SPEC_IGNORE_FILE, false)
	if err != nil {
		return fmt.Errorf("error reading spec in '%v': %w", specDir, err)
	}

	exists, err := fr.ExistsInSpecs(resource)
	if err != nil {
		return err
	}

	if exists {
		return fmt.Errorf("same name resource (%v) already exists in namespace (%v)", meta.Name, meta.Namespace)
	}

	truncate := update
	err = save(data, specDir, specFile, truncate)
	if err != nil {
		return err
	}

	console.Info(fmt.Sprintf("Saving %v '%v/%v' to '%v/%v'",
		kind, meta.Namespace, meta.Name, specDir, specFile))

	return nil
}

func SpecDry(resource interface{}) error {
	_, _, data, err := crdToYaml(resource)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func crdToYaml(resource interface{}) (metav1.ObjectMeta, string, []byte, error) {
	// make sure we're writing a known type
	var meta metav1.ObjectMeta
	var kind string
	var data []byte
	var err error

	switch typedres := resource.(type) {
	case types.ArchiveUploadSpec:
		typedres.Kind = "ArchiveUploadSpec"
		meta = metav1.ObjectMeta{
			Name: typedres.Name,
		}
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.Package:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "Package"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.Function:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "Function"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.Environment:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "Environment"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.HTTPTrigger:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "HTTPTrigger"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.KubernetesWatchTrigger:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "KubernetesWatchTrigger"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.MessageQueueTrigger:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "MessageQueueTrigger"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	case fv1.TimeTrigger:
		typedres.APIVersion = fv1.CRD_VERSION
		typedres.Kind = "TimeTrigger"
		meta = typedres.ObjectMeta
		kind = typedres.Kind
		data, err = yaml.Marshal(typedres)
	default:
		err = fmt.Errorf("unknown object type '%v'", typedres)
	}

	if err != nil {
		return metav1.ObjectMeta{}, "", nil, fmt.Errorf("couldn't marshal YAML: %w", err)
	}

	return meta, kind, data, nil
}

// validateFunctionReference checks a function reference
func (fr *FissionResources) validateFunctionReference(functions map[string]bool, kind string, meta *metav1.ObjectMeta, funcRef fv1.FunctionReference) error {
	if funcRef.Type == fv1.FunctionReferenceTypeFunctionName {
		// triggers only reference functions in their own namespace
		namespace := meta.Namespace
		name := funcRef.Name
		m := &metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		}
		if _, ok := functions[k8sCache.MetaObjectToName(m).String()]; !ok {
			return fmt.Errorf("%v: %v '%v' references unknown function '%v'",
				fr.SourceMap.Locations[kind][meta.Namespace][meta.Name],
				kind,
				meta.Name,
				name)
		} else {
			functions[k8sCache.MetaObjectToName(m).String()] = true
		}
	}
	return nil
}

// Validate validates the spec file for irregular references
func (fr *FissionResources) Validate(input cli.Input, client cmd.Client) ([]string, error) {
	result := utils.MultiErrorWithFormat()
	var warnings []string

	// check references: both dangling refs + garbage
	//   packages -> archives
	//   functions -> packages
	//   functions -> environments + shared environments between functions [TODO]
	//   functions -> secrets + configmaps (same ns) [TODO]
	//   triggers -> functions

	// index archives
	archives := make(map[string]bool)
	for _, a := range fr.ArchiveUploadSpecs {
		archives[a.Name] = false
	}

	// index packages, check outgoing refs, mark archives that are referenced
	packages := make(map[string]bool)
	for _, p := range fr.Packages {
		packages[k8sCache.MetaObjectToName(&p.ObjectMeta).String()] = false

		as := map[string]string{
			"source":     p.Spec.Source.URL,
			"deployment": p.Spec.Deployment.URL,
		}

		for archiveType, u := range as {
			if strings.HasPrefix(u, ARCHIVE_URL_PREFIX) {
				aname := strings.TrimPrefix(u, ARCHIVE_URL_PREFIX)
				if len(aname) > 0 {
					if _, ok := archives[aname]; !ok {
						result = multierror.Append(result, fmt.Errorf(
							"%v: package '%v' references unknown %v archive '%v%v'",
							fr.SourceMap.Locations["Package"][p.ObjectMeta.Namespace][p.ObjectMeta.Name],
							p.ObjectMeta.Name,
							archiveType,
							ARCHIVE_URL_PREFIX,
							aname))
					} else {
						archives[aname] = true
					}
				}
			}
		}

		result = multierror.Append(result, p.Validate())
	}

	// error on unreferenced archives
	for name, referenced := range archives {
		if !referenced {
			result = multierror.Append(result, fmt.Errorf(
				"%v: archive '%v' is not used in any package",
				fr.SourceMap.Locations["ArchiveUploadSpec"][""][name],
				name))
		}
	}

	// index functions, check function package refs, mark referenced packages
	functions := make(map[string]bool)
	for _, f := range fr.Functions {
		functions[k8sCache.MetaObjectToName(&f.ObjectMeta).String()] = false

		if f.Spec.InvokeStrategy.ExecutionStrategy.ExecutorType != fv1.ExecutorTypeContainer {
			pkgMeta := &metav1.ObjectMeta{
				Name:      f.Spec.Package.PackageRef.Name,
				Namespace: f.Spec.Package.PackageRef.Namespace,
			}

			// check package ref from function
			packageRefExists := func() bool {
				_, ok := packages[k8sCache.MetaObjectToName(pkgMeta).String()]
				return ok
			}

			// check that the package referenced by each function is in the same ns as the function
			packageRefInFuncNs := func(f *fv1.Function) bool {
				return f.Spec.Package.PackageRef.Namespace == f.ObjectMeta.Namespace
			}

			if !packageRefInFuncNs(&f) {
				result = multierror.Append(result, fmt.Errorf(
					"%v: function '%v' references a package outside of its namespace %v/%v",
					fr.SourceMap.Locations["Function"][f.ObjectMeta.Namespace][f.ObjectMeta.Name],
					f.ObjectMeta.Name,
					f.Spec.Package.PackageRef.Namespace,
					f.Spec.Package.PackageRef.Name))
			} else if !packageRefExists() {
				result = multierror.Append(result, fmt.Errorf(
					"%v: function '%v' references unknown package %v/%v",
					fr.SourceMap.Locations["Function"][f.ObjectMeta.Namespace][f.ObjectMeta.Name],
					f.ObjectMeta.Name,
					pkgMeta.Namespace,
					pkgMeta.Name))
			} else {
				packages[k8sCache.MetaObjectToName(pkgMeta).String()] = true
			}
		}

		for _, cm := range f.Spec.ConfigMaps {

			err := util.ConfigMapExists(input.Context(), &metav1.ObjectMeta{Namespace: cm.Namespace, Name: cm.Name}, client.KubernetesClient)
			if k8serrors.IsNotFound(err) {
				warnings = append(warnings, fmt.Sprintf("Configmap %s is referred in the spec but not present in the cluster", cm.Name))
			}
		}

		for _, s := range f.Spec.Secrets {
			err := util.SecretExists(input.Context(), &metav1.ObjectMeta{Namespace: s.Namespace, Name: s.Name}, client.KubernetesClient)

			if k8serrors.IsNotFound(err) {
				warnings = append(warnings, fmt.Sprintf("Secret %s is referred in the spec but not present in the cluster", s.Name))
			}
		}
		result = multierror.Append(result, f.Validate())
	}

	// error on unreferenced packages
	for key, referenced := range packages {
		namespace, name, err := k8sCache.SplitMetaNamespaceKey(key)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to check the reference for the package '%s'", key))
		}
		if !referenced {
			warnings = append(warnings, fmt.Sprintf(
				"%v: package '%v' is not used in any function",
				fr.SourceMap.Locations["Package"][namespace][name],
				name))
		}
	}

	// check function refs from triggers
	for _, t := range fr.HttpTriggers {
		err := fr.validateFunctionReference(functions, t.Kind, &t.ObjectMeta, t.Spec.FunctionReference)
		if err != nil {
			result = multierror.Append(result, err)
		}

		if len(t.Spec.Host) > 0 {
			warnings = append(warnings, "Host in HTTPTrigger spec.Host is now marked as deprecated, see 'help' for details")
		}
		if len(t.Spec.Method) > 0 {
			warnings = append(warnings, "Method in HTTTPTrigger spec.Method is deprecated, use spec.Methods instead")
		}
		result = multierror.Append(result, t.Validate())
	}
	for _, t := range fr.KubernetesWatchTriggers {
		err := fr.validateFunctionReference(functions, t.Kind, &t.ObjectMeta, t.Spec.FunctionReference)
		if err != nil {
			result = multierror.Append(result, err)
		}
		result = multierror.Append(result, t.Validate())
	}
	for _, t := range fr.TimeTriggers {
		err := fr.validateFunctionReference(functions, t.Kind, &t.ObjectMeta, t.Spec.FunctionReference)
		if err != nil {
			result = multierror.Append(result, err)
		}
		result = multierror.Append(result, t.Validate())
	}
	for _, t := range fr.MessageQueueTriggers {
		err := fr.validateFunctionReference(functions, t.Kind, &t.ObjectMeta, t.Spec.FunctionReference)
		if err != nil {
			result = multierror.Append(result, err)
		}
		result = multierror.Append(result, t.Validate())
	}

	// we do not error on unreferenced functions (you can call a function through workflows,
	// `fission function test`, etc.)

	// Index envs, warn on functions referencing an environment for which spes does not exist
	environments := make(map[string]struct{})
	for _, e := range fr.Environments {
		environments[fmt.Sprintf("%s:%s", e.ObjectMeta.Name, e.ObjectMeta.Namespace)] = struct{}{}
		if ((e.Spec.Runtime.Container != nil) && (e.Spec.Runtime.PodSpec != nil)) || ((e.Spec.Builder.Container != nil) && (e.Spec.Builder.PodSpec != nil)) {
			warnings = append(warnings, "You have provided both - container spec and pod spec and while merging the pod spec will take precedence.")
			if e.Spec.Runtime.Container.Name != "" && e.Spec.Runtime.PodSpec != nil {
				if !executorUtil.DoesContainerExistInPodSpec(e.Spec.Runtime.Container.Name, e.Spec.Runtime.PodSpec) {
					result = multierror.Append(result, fmt.Errorf("runtime container %s does not exist in the pod spec", e.Spec.Runtime.Container.Name))
				}
			}
		}
		// Unlike CLI can change the environment version silently,
		// we have to warn the user to modify spec file when this takes place.
		if e.Spec.Poolsize != 3 && e.Spec.Version < 3 {
			warnings = append(warnings, "Poolsize can only be configured when environment version equals to 3, default poolsize 3 will be used for creating environment pool.")
		}
	}

	for _, f := range fr.Functions {
		if _, ok := environments[fmt.Sprintf("%s:%s", f.Spec.Environment.Name, f.Spec.Environment.Namespace)]; !ok {
			warnings = append(warnings, fmt.Sprintf("Environment %s is referenced in function %s but not declared in specs", f.Spec.Environment.Name, f.ObjectMeta.Name))
		}
		strategy := f.Spec.InvokeStrategy.ExecutionStrategy
		if strategy.ExecutorType == fv1.ExecutorTypeNewdeploy && strategy.SpecializationTimeout < fv1.DefaultSpecializationTimeOut {
			warnings = append(warnings, fmt.Sprintf("SpecializationTimeout in function spec.InvokeStrategy.ExecutionStrategy should be a value equal to or greater than %v", fv1.DefaultSpecializationTimeOut))
		}
		if f.Spec.FunctionTimeout <= 0 {
			warnings = append(warnings, "FunctionTimeout in function spec should be a field which should have a value greater than 0")
		}
	}
	// (ErrorOrNil returns nil if there were no errors appended.)
	return warnings, result.ErrorOrNil()
}

// Keep track of source location of resources, and track duplicates
func (fr *FissionResources) trackSourceMap(kind string, newobj *metav1.ObjectMeta, loc *Location) error {
	if _, exists := fr.SourceMap.Locations[kind]; !exists {
		fr.SourceMap.Locations[kind] = make(map[string](map[string]Location))
	}
	if _, exists := fr.SourceMap.Locations[kind][newobj.Namespace]; !exists {
		fr.SourceMap.Locations[kind][newobj.Namespace] = make(map[string]Location)
	}

	// check for duplicate resources
	oldloc, exists := fr.SourceMap.Locations[kind][newobj.Namespace][newobj.Name]
	if exists {
		return fmt.Errorf("%v: Duplicate %v '%v', first defined in %v", loc, kind, newobj.Name, oldloc)
	}

	// track new resource
	fr.SourceMap.Locations[kind][newobj.Namespace][newobj.Name] = *loc

	return nil
}

// Apply commit label to the object metadata
func applyCommitLabel(commitLabelVal string, m *metav1.ObjectMeta) {
	if len(commitLabelVal) != 0 {
		if m.Labels == nil {
			m.Labels = make(map[string]string)
		}
		m.Labels[util.COMMIT_LABEL] = commitLabelVal
	}
}

// ParseYaml takes one yaml document, figures out its type, parses it, and puts it in
// the right list in the given fission resources set.
func (fr *FissionResources) ParseYaml(b []byte, loc *Location, commitLabelVal string) error {
	var m *metav1.ObjectMeta

	// Figure out the object type by unmarshaling into the TypeMeta struct; then
	// unmarshal again into the "real" struct once we know the type.
	var tm types.TypeMeta
	err := yaml.Unmarshal(b, &tm)
	if err != nil {
		return fmt.Errorf("failed to decode yaml %s: %w", string(b), err)
	}

	switch tm.Kind {
	case "Package":
		var v fv1.Package
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.Packages = append(fr.Packages, v)
	case "Function":
		var v fv1.Function
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.Functions = append(fr.Functions, v)
	case "Environment":
		var v fv1.Environment
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.Environments = append(fr.Environments, v)
	case "HTTPTrigger":
		var v fv1.HTTPTrigger
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.HttpTriggers = append(fr.HttpTriggers, v)
	case "KubernetesWatchTrigger":
		var v fv1.KubernetesWatchTrigger
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.KubernetesWatchTriggers = append(fr.KubernetesWatchTriggers, v)
	case "TimeTrigger":
		var v fv1.TimeTrigger
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.TimeTriggers = append(fr.TimeTriggers, v)
	case "MessageQueueTrigger":
		var v fv1.MessageQueueTrigger
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}
		m = &v.ObjectMeta
		applyCommitLabel(commitLabelVal, m)
		fr.MessageQueueTriggers = append(fr.MessageQueueTriggers, v)

	// The following are not CRDs

	case "DeploymentConfig":
		var v types.DeploymentConfig
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}

		fr.DeploymentConfig = v
	case "ArchiveUploadSpec":
		var v types.ArchiveUploadSpec
		err = yaml.Unmarshal(b, &v)
		if err != nil {
			return fmt.Errorf("failed to parse %v in %v: %w", tm.Kind, loc, err)
		}

		m = &metav1.ObjectMeta{
			Name:      v.Name,
			Namespace: "",
		}
		applyCommitLabel(commitLabelVal, m)
		fr.ArchiveUploadSpecs = append(fr.ArchiveUploadSpecs, v)
	default:
		// no need to error out just because there's some extra files around;
		// also good for compatibility.
		console.Warn(fmt.Sprintf("Ignoring unknown type %v in %v", tm.Kind, loc))
	}

	// add to source map, check for duplicates
	if m != nil {
		err = fr.trackSourceMap(tm.Kind, m, loc)
		if err != nil {
			return err
		}
	}

	return nil
}

// Returns metadata if the given resource exists in the specs, nil
// otherwise.  compareMetadata and compareSpec control how the
// equality check is performed.
// TODO: deprecated SpecExists
func (fr *FissionResources) SpecExists(resource interface{}, compareMetadata bool, compareSpec bool) interface{} {
	switch typedres := resource.(type) {
	case *types.ArchiveUploadSpec:
		for _, aus := range fr.ArchiveUploadSpecs {
			if compareMetadata && aus.Name != typedres.Name {
				continue
			}
			if compareSpec &&
				!(reflect.DeepEqual(aus.RootDir, typedres.RootDir) &&
					reflect.DeepEqual(aus.IncludeGlobs, typedres.IncludeGlobs) &&
					reflect.DeepEqual(aus.ExcludeGlobs, typedres.ExcludeGlobs)) {
				continue
			}
			return &aus
		}
		return nil
	case *fv1.Package:
		for _, p := range fr.Packages {
			if compareMetadata && !reflect.DeepEqual(p.ObjectMeta, typedres.ObjectMeta) {
				continue
			}
			if compareSpec && !reflect.DeepEqual(p.Spec, typedres.Spec) {
				continue
			}
			return &p
		}
		return nil

	default:
		// XXX not implemented
		return nil
	}
}

func (fr *FissionResources) ExistsInSpecs(resource interface{}) (bool, error) {
	switch typedres := resource.(type) {
	case types.ArchiveUploadSpec:
		for _, obj := range fr.ArchiveUploadSpecs {
			if obj.Name == typedres.Name {
				return true, nil
			}
		}
	case fv1.Package:
		for _, obj := range fr.Packages {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.Function:
		for _, obj := range fr.Functions {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.Environment:
		for _, obj := range fr.Environments {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.HTTPTrigger:
		for _, obj := range fr.HttpTriggers {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.KubernetesWatchTrigger:
		for _, obj := range fr.KubernetesWatchTriggers {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.MessageQueueTrigger:
		for _, obj := range fr.MessageQueueTriggers {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	case fv1.TimeTrigger:
		for _, obj := range fr.TimeTriggers {
			if obj.ObjectMeta.Name == typedres.ObjectMeta.Name &&
				obj.ObjectMeta.Namespace == typedres.ObjectMeta.Namespace {
				return true, nil
			}
		}
	default:
		return false, fmt.Errorf("unknown resource type %#v", typedres)
	}

	return false, nil
}

func (loc Location) String() string {
	return fmt.Sprintf("%v:%v", loc.Path, loc.Line)
}
