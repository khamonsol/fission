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

package _package

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"errors"

	"github.com/dchest/uniuri"
	"github.com/hashicorp/go-multierror"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/cli"
	"github.com/fission/fission/pkg/fission-cli/cmd"
	pkgutil "github.com/fission/fission/pkg/fission-cli/cmd/package/util"
	"github.com/fission/fission/pkg/fission-cli/cmd/spec"
	spectypes "github.com/fission/fission/pkg/fission-cli/cmd/spec/types"
	"github.com/fission/fission/pkg/fission-cli/console"
	flagkey "github.com/fission/fission/pkg/fission-cli/flag/key"
	"github.com/fission/fission/pkg/fission-cli/util"
	"github.com/fission/fission/pkg/utils"
	"github.com/fission/fission/pkg/utils/uuid"
)

// CreateArchive returns a fv1.Archive made from an archive .  If specFile, then
// create an archive upload spec in the specs directory; otherwise
// upload the archive using client.  noZip avoids zipping the
// includeFiles, but is ignored if there's more than one includeFile.
func CreateArchive(client cmd.Client, input cli.Input, includeFiles []string, noZip bool, insecure bool, checksum string, specDir string, specFile string) (*fv1.Archive, error) {
	// get root dir
	var rootDir string
	var err error

	if len(specFile) > 0 {
		rootDir, err = filepath.Abs(specDir + "/..")
		if err != nil {
			return nil, fmt.Errorf("error getting root directory of spec directory: %w", err)
		}
	}
	errs := utils.MultiErrorWithFormat()
	fileURL := ""

	// check files existence
	for _, path := range includeFiles {
		// ignore http files
		if utils.IsURL(path) {
			if len(includeFiles) > 1 {
				// It's intentional to disallow the user to provide file and URL at the same time.
				return nil, errors.New("unable to create an archive that contains both file and URL")
			}
			fileURL = path
			break
		}

		// Get files from inputs as number of files decide next steps
		absPath, err := filepath.Abs(path)
		if err != nil {
			errs = multierror.Append(errs, fmt.Errorf("error converting path to the absolute path \"%v\": %w", path, err))
			continue
		}

		if !strings.HasPrefix(absPath, rootDir) {
			errs = multierror.Append(errs, fmt.Errorf("the files (%v) should be put under the same parent directory (%v) of spec directory; otherwise, the archive will be empty when applying spec files", path, rootDir))
			continue
		}

		path := filepath.Join(rootDir, path)
		files, err := utils.FindAllGlobs(path)
		if err != nil {
			errs = multierror.Append(errs, fmt.Errorf("error finding all globs: %w", err))
			continue
		}

		if len(files) == 0 {
			errs = multierror.Append(errs, fmt.Errorf("error finding any files with path \"%v\"", path))
		}
	}

	if errs.ErrorOrNil() != nil {
		return nil, errs.ErrorOrNil()
	}

	if len(fileURL) > 0 {
		if insecure {
			return &fv1.Archive{
				Type: fv1.ArchiveTypeUrl,
				URL:  fileURL,
			}, nil
		}

		var csum *fv1.Checksum

		if len(checksum) > 0 {
			csum = &fv1.Checksum{
				Type: fv1.ChecksumTypeSHA256,
				Sum:  checksum,
			}
		} else {
			console.Info(fmt.Sprintf("Downloading file to generate SHA256 checksum. To skip this step, please use --%v / --%v / --%v",
				flagkey.PkgSrcChecksum, flagkey.PkgDeployChecksum, flagkey.PkgInsecure))

			tmpDir, err := utils.GetTempDir()
			if err != nil {
				return nil, err
			}

			file := filepath.Join(tmpDir, uuid.NewString())
			err = utils.DownloadUrl(input.Context(), http.DefaultClient, fileURL, file)
			if err != nil {
				return nil, fmt.Errorf("error downloading file from the given URL: %w", err)
			}

			csum, err = utils.GetFileChecksum(file)
			if err != nil {
				return nil, fmt.Errorf("error generating file SHA256 checksum: %w", err)
			}
		}

		return &fv1.Archive{
			Type:     fv1.ArchiveTypeUrl,
			URL:      fileURL,
			Checksum: *csum,
		}, nil
	}

	if input.Bool(flagkey.SpecSave) || input.Bool(flagkey.SpecDry) {
		// create an ArchiveUploadSpec and reference it from the archive
		aus := &spectypes.ArchiveUploadSpec{
			Name:         archiveName("", includeFiles),
			IncludeGlobs: includeFiles,
		}

		if input.Bool(flagkey.SpecDry) {
			err := spec.SpecDry(*aus)
			if err != nil {
				return nil, err
			}
		} else if input.Bool(flagkey.SpecSave) {
			// check if this AUS exists in the specs; if so, don't create a new one
			specIgnore := util.GetSpecIgnore(input)
			fr, err := spec.ReadSpecs(specDir, specIgnore, false)
			if err != nil {
				return nil, fmt.Errorf("error reading specs: %w", err)
			}

			obj := fr.SpecExists(aus, true, true)
			if obj != nil {
				oldAus := obj.(*spectypes.ArchiveUploadSpec)
				fmt.Printf("Re-using previously created archive %v\n", oldAus.Name)
				aus.Name = oldAus.Name
			} else {
				// save the uploadspec
				err := spec.SpecSave(*aus, specFile, false)
				if err != nil {
					return nil, fmt.Errorf("error saving archive spec: %w", err)
				}
			}
		}

		// create the archive object
		archive := fv1.Archive{
			Type: fv1.ArchiveTypeUrl,
			URL:  fmt.Sprintf("%v%v", spec.ARCHIVE_URL_PREFIX, aus.Name),
		}
		return &archive, nil
	}

	archivePath, err := makeArchiveFile(input.Context(), "", includeFiles, noZip)
	if err != nil {
		return nil, err
	}

	return pkgutil.UploadArchiveFile(input.Context(), client, archivePath)
}

// makeArchiveFile creates a zip file from the given list of input files,
// unless that list has only one item and that item is a zip file.
//
// If the inputs have only one file and noZip is true, the file is
// returned as-is with no zipping.  (This is used for compatibility
// with v1 envs.)  noZip is IGNORED if there is more than one input
// file.
func makeArchiveFile(ctx context.Context, archiveNameHint string, archiveInput []string, noZip bool) (string, error) {

	// Unique name for the archive
	archiveFileName := archiveName(archiveNameHint, archiveInput) + ".zip"

	// Get files from inputs as number of files decide next steps
	files, err := utils.FindAllGlobs(archiveInput...)
	if err != nil {
		return "", fmt.Errorf("error finding all globs: %w", err)
	}

	// We have one file; if it's a zip file, no need to archive it
	if len(files) == 1 {
		// make sure it exists
		if _, err := os.Stat(files[0]); err != nil {
			return "", fmt.Errorf("open input file %v: %w", files[0], err)
		}

		// if it's an existing zip file OR we're not supposed to zip it, don't do anything
		if match, _ := utils.IsZip(ctx, files[0]); match || noZip {
			return files[0], nil
		}
	}

	// For anything else, create a new archive
	tmpDir, err := utils.GetTempDir()
	if err != nil {
		return "", fmt.Errorf("error create temporary archive directory: %w", err)
	}

	archivePath, err := utils.MakeZipArchiveWithGlobs(ctx, filepath.Join(tmpDir, archiveFileName), archiveInput...)
	if err != nil {
		return "", fmt.Errorf("create archive file: %w", err)
	}

	return archivePath, nil
}

// Name an archive
func archiveName(givenNameHint string, includedFiles []string) string {
	if len(givenNameHint) > 0 {
		return fmt.Sprintf("%v-%v", givenNameHint, uniuri.NewLen(4))
	}
	if len(includedFiles) == 0 {
		return uniuri.NewLen(8)
	}
	return fmt.Sprintf("%v-%v", util.KubifyName(includedFiles[0]), uniuri.NewLen(4))
}

func GetFunctionsByPackage(ctx context.Context, client cmd.Client, pkgName, pkgNamespace string) ([]fv1.Function, error) {
	fnList, err := client.FissionClientSet.CoreV1().Functions(pkgNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return nil, err
	}
	fns := []fv1.Function{}
	for _, fn := range fnList.Items {
		if fn.Spec.Package.PackageRef.Name == pkgName {
			fns = append(fns, fn)
		}
	}
	return fns, nil
}
