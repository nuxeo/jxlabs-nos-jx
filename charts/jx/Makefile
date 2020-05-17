CHART_REPO := gs://jxlabs-nos-charts
NAME := jx

os := $(shell uname 2>/dev/null || echo Unknown)
ifeq ($(os), Darwin)
helm-plugins-dir := $(HOME)/Library/Caches/helm/plugins
else
helm-plugins-dir := $(HOME)/.cache/helm/plugins
endif

$(helm-plugins-dir)/https-github.com-viglesiasce-helm-gcs.git:
	helm plugin install https://github.com/viglesiasce/helm-gcs.git --version v0.2.0

build: clean
	rm -rf Chart.lock
	helm dependency build
	helm lint

install: clean build
	helm install . --name ${NAME}

upgrade: clean build
	helm upgrade ${NAME} .

delete:
	helm delete --purge ${NAME}

clean:
	rm -rf charts
	rm -rf ${NAME}*.tgz


release: clean | $(helm-plugins-dir)/https-github.com-viglesiasce-helm-gcs.git
	sed -i -e "s/version:.*/version: $(VERSION)/" Chart.yaml
	helm dependency build
	helm lint
	helm package .
	helm gcs push ${NAME}*.tgz $(CHART_REPO)
	rm -rf ${NAME}*.tgz%

