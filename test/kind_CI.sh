#!/bin/bash

set -euo pipefail

if [ ! -f ${HOME}/.kube/config ]
then
    echo "Skipping end to end tests, no cluster credentials"
    exit 0
fi

source ./test/test_utils.sh

echo "source test_utils done"

dump_system_info

export FUNCTION_NAMESPACE=${FUNCTION_NAMESPACE:-default}
export BUILDER_NAMESPACE=${BUILDER_NAMESPACE:-default}
export FISSION_NAMESPACE=${FISSION_NAMESPACE:-fission}
export FISSION_ROUTER=127.0.0.1:8888

# Images used in the tests
export REPOSITORY=ghcr.io/fission
export NODE_RUNTIME_IMAGE=${REPOSITORY}/node-env-22
export NODE_BUILDER_IMAGE=${REPOSITORY}/node-builder-22
export PYTHON_RUNTIME_IMAGE=${REPOSITORY}/python-env
export PYTHON_BUILDER_IMAGE=${REPOSITORY}/python-builder
export GO_RUNTIME_IMAGE=${REPOSITORY}/go-env-1.23
export GO_BUILDER_IMAGE=${REPOSITORY}/go-builder-1.23
export JVM_RUNTIME_IMAGE=${REPOSITORY}/jvm-env
export JVM_BUILDER_IMAGE=${REPOSITORY}/jvm-builder
export JVM_JERSEY_RUNTIME_IMAGE=${REPOSITORY}/jvm-jersey-env-22
export JVM_JERSEY_BUILDER_IMAGE=${REPOSITORY}/jvm-jersey-builder-22
export TS_RUNTIME_IMAGE=${REPOSITORY}/tensorflow-serving-env

export CONTROLLER_IP=127.0.0.1:8889
export FISSION_NATS_STREAMING_URL=http://defaultFissionAuthToken@127.0.0.1:8890

echo "Variables set"
echo "FUNCTION_NAMESPACE: $FUNCTION_NAMESPACE"
echo "BUILDER_NAMESPACE: $BUILDER_NAMESPACE"
echo "FISSION_NAMESPACE: $FISSION_NAMESPACE"

echo "Pulling env and builder images"
docker pull -q $NODE_RUNTIME_IMAGE && kind load docker-image $NODE_RUNTIME_IMAGE --name kind
docker pull -q $NODE_BUILDER_IMAGE && kind load docker-image $NODE_BUILDER_IMAGE --name kind
docker system prune -a -f > /dev/null
docker pull -q $PYTHON_RUNTIME_IMAGE && kind load docker-image $PYTHON_RUNTIME_IMAGE --name kind
docker pull -q $PYTHON_BUILDER_IMAGE && kind load docker-image $PYTHON_BUILDER_IMAGE --name kind
docker pull -q $JVM_RUNTIME_IMAGE && kind load docker-image $JVM_RUNTIME_IMAGE --name kind
docker pull -q $JVM_BUILDER_IMAGE && kind load docker-image $JVM_BUILDER_IMAGE --name kind
docker system prune -a -f > /dev/null
docker pull -q $JVM_JERSEY_RUNTIME_IMAGE && kind load docker-image $JVM_JERSEY_RUNTIME_IMAGE --name kind
docker pull -q $JVM_JERSEY_BUILDER_IMAGE && kind load docker-image $JVM_JERSEY_BUILDER_IMAGE --name kind
docker pull -q $GO_RUNTIME_IMAGE && kind load docker-image $GO_RUNTIME_IMAGE --name kind
docker pull -q $GO_BUILDER_IMAGE && kind load docker-image $GO_BUILDER_IMAGE --name kind
docker system prune -a -f > /dev/null
docker pull -q $TS_RUNTIME_IMAGE && kind load docker-image $TS_RUNTIME_IMAGE --name kind
echo "Successfully pull env and builder images"

# run tests without newdeploy in parallel.

export FAILURES=0
main() {
    set +e
    export TIMEOUT=900  # 15 minutes per test
    # run tests without newdeploy in parallel.
    export JOBS=6
    source $ROOT/test/run_test.sh \
        $ROOT/test/tests/test_archive_cli.sh \
        $ROOT/test/tests/test_canary.sh \
        $ROOT/test/tests/test_fn_update/test_idle_objects_reaper.sh \
        $ROOT/test/tests/test_annotations.sh \
        $ROOT/test/tests/test_archive_pruner.sh \
        $ROOT/test/tests/test_backend_poolmgr.sh \
        $ROOT/test/tests/test_buildermgr.sh \
        $ROOT/test/tests/test_env_vars.sh \
        $ROOT/test/tests/test_env_podspec.sh \
        $ROOT/test/tests/test_environments/test_python_env.sh \
        $ROOT/test/tests/test_function_test/test_fn_test.sh \
        $ROOT/test/tests/test_function_update.sh \
        $ROOT/test/tests/test_ingress.sh \
        $ROOT/test/tests/test_internal_routes.sh \
        $ROOT/test/tests/test_logging/test_function_logs.sh \
        $ROOT/test/tests/test_node_hello_http.sh \
        $ROOT/test/tests/test_package_command.sh \
        $ROOT/test/tests/test_package_checksum.sh \
        $ROOT/test/tests/test_pass.sh \
        $ROOT/test/tests/test_specs/test_spec.sh \
        $ROOT/test/tests/test_specs/test_spec_multifile.sh \
        $ROOT/test/tests/test_specs/test_spec_merge/test_spec_merge.sh \
        $ROOT/test/tests/test_specs/test_spec_archive/test_spec_archive.sh \
        $ROOT/test/tests/test_environments/test_tensorflow_serving_env.sh \
        $ROOT/test/tests/test_environments/test_go_env.sh \
        $ROOT/test/tests/test_huge_response/test_huge_response.sh \
        $ROOT/test/tests/test_kubectl/test_kubectl.sh \
        $ROOT/test/tests/websocket/test_ws.sh

    export JOBS=3
    source $ROOT/test/run_test.sh \
        $ROOT/test/tests/test_backend_newdeploy.sh \
        $ROOT/test/tests/test_fn_update/test_scale_change.sh \
        $ROOT/test/tests/test_secret_cfgmap/test_secret_cfgmap.sh \
        $ROOT/test/tests/test_environments/test_java_builder.sh \
        $ROOT/test/tests/test_environments/test_java_env.sh \
        $ROOT/test/tests/test_environments/test_nodejs_env.sh \
        $ROOT/test/tests/test_fn_update/test_configmap_update.sh \
        $ROOT/test/tests/test_fn_update/test_env_update.sh \
        $ROOT/test/tests/test_obj_create_in_diff_ns.sh \
        $ROOT/test/tests/test_fn_update/test_resource_change.sh \
        $ROOT/test/tests/test_fn_update/test_secret_update.sh \
        $ROOT/test/tests/test_fn_update/test_nd_pkg_update.sh \
        $ROOT/test/tests/test_fn_update/test_poolmgr_nd.sh  
        $ROOT/test/tests/test_namespace/test_ns_current_context.sh
        $ROOT/test/tests/test_namespace/test_ns_flag.sh
        $ROOT/test/tests/test_namespace/test_ns_env.sh
        $ROOT/test/tests/test_namespace/test_ns_deprecated_flag.sh

    set -e

    # dump test logs
    # TODO: the idx does not match seq number in recap.
    idx=1
    log_files=$(find test/logs/ -name '*.log')

    for log_file in $log_files; do
        test_name=${log_file#test/logs/}
        # travis_fold_start run_test.$idx $test_name
        echo "========== start $test_name =========="
        cat $log_file
        echo "========== end $test_name =========="
        # travis_fold_end run_test.$idx
        idx=$((idx+1))
    done
}

main

echo "Total Failures" $FAILURES
if [[ $FAILURES != '0' ]]; then
    exit 1
fi
