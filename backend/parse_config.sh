#!/bin/sh

export SERVER_HOST=$(yq e '.server.host' config.yaml)
export SERVER_PORT=$(yq e '.server.port' config.yaml)
export SERVER_MODE=$(yq e '.server.mode' config.yaml)
export CODE_DIR=$(yq e '.paths.code_dir' config.yaml)
export PERSISTENT_CODE_DIR=$(yq e '.paths.persistent_code_dir' config.yaml)
export FRONTEND_DIR=$(yq e '.paths.frontend_dir' config.yaml)
export DOCKER_IMAGE=$(yq e '.docker.image_name' config.yaml)



exec "$@"