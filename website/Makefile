SHELL := /bin/bash
GITBRANCH = $(shell git rev-parse --abbrev-ref HEAD)
GITHASH = $(shell git rev-parse --short HEAD)
GITTAG = $(shell git describe --tags --exact-match HEAD 2> /dev/null)
GITTAG_CLEAN = $(subst v,,$(GITTAG))
BUILD_TAG = $(if $(GITTAG),$(GITTAG_CLEAN),$(GITBRANCH)-$(GITHASH))

DOCKER_REPOSITORY = 347633553755.dkr.ecr.us-west-2.amazonaws.com
DOCKER_LOCAL_REPOSITORY = localhost:5000

PROJECT_NAME = exohub-website
DEV_HOST ?= localhost
BUILD_IMAGE_NAME = gp/${PROJECT_NAME}

.build:
	#docker build -t python-arvados:1.0 .
	docker build \
	--build-arg BASE_IMAGE=${DOCKER_REPOSITORY}/${GPAPY_BASE_IMAGE} \
	--build-arg GITHASH=${GITHASH} \
	--build-arg GITBRANCH=${GITBRANCH} \
	-t ${BUILD_IMAGE_NAME}:${BUILD_TAG} \
	-f Dockerfile .

image: .build

push: #.validate_git_commit
	docker tag ${BUILD_IMAGE_NAME}:${BUILD_TAG} ${DOCKER_REPOSITORY}/${BUILD_IMAGE_NAME}:${BUILD_TAG}
	docker push ${DOCKER_REPOSITORY}/${BUILD_IMAGE_NAME}:${BUILD_TAG}

dev:
	hugo --environment internal --minify
	npx -y pagefind --site public
	hugo server --environment internal --bind 0.0.0.0 --port 8000 --baseURL http://$(DEV_HOST):8000/

dev-push:
	docker tag ${BUILD_IMAGE_NAME}:${BUILD_TAG} ${DOCKER_LOCAL_REPOSITORY}/${BUILD_IMAGE_NAME}:${BUILD_TAG}
	docker tag ${BUILD_IMAGE_NAME}:${BUILD_TAG} ${DOCKER_LOCAL_REPOSITORY}/${BUILD_IMAGE_NAME}:latest
	docker push ${DOCKER_LOCAL_REPOSITORY}/${BUILD_IMAGE_NAME}:${BUILD_TAG}
	docker push ${DOCKER_LOCAL_REPOSITORY}/${BUILD_IMAGE_NAME}:latest

oss-build:
	hugo --environment oss --minify
	npx -y pagefind --site public

build-oss-check: oss-build
	bash scripts/leak-scan.sh public

ingest:
	gitingest -i content
