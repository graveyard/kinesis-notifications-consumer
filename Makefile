include golang.mk
.DEFAULT_GOAL := test # override default goal set in library makefile

.PHONY: all download_jars test build clean
SHELL := /bin/bash
JAR_DIR := jars
PKGS = $(shell GO15VENDOREXPERIMENT=1 go list ./... | grep -v "vendor/")
BINARY_NAME := kinesis-notifications-consumer
$(eval $(call golang-version-check,1.9))

TMP_DIR := /tmp/kinesis-notifications-consumer-jars
JAR_DIR := ./jars
KCL_VERSION := 1.8.10

define POM_XML_FOR_GETTING_DEPENDENT_JARS
<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
  xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.clever.kinesisconsumers</groupId>
  <artifactId>$(BINARY_NAME)</artifactId>
  <version>1.0-SNAPSHOT</version>
  <dependencies>
    <dependency>
      <groupId>com.amazonaws</groupId>
      <artifactId>amazon-kinesis-client</artifactId>
      <version>$(KCL_VERSION)</version>
    </dependency>
  </dependencies>
</project>
endef
export POM_XML_FOR_GETTING_DEPENDENT_JARS
download_jars:
	command -v mvn >/dev/null 2>&1 || { echo >&2 "Maven not installed. Install maven!"; exit 1; }
	mkdir -p $(JAR_DIR)/ $(TMP_DIR)
	rm -f $(JAR_DIR)/*
	echo $$POM_XML_FOR_GETTING_DEPENDENT_JARS > $(TMP_DIR)/pom.xml
	cd $(TMP_DIR) && mvn dependency:copy-dependencies
	mv $(TMP_DIR)/target/dependency/* $(JAR_DIR)/
	# Download the STS jar file for supporting IAM Roles
	ls $(JAR_DIR)/aws-java-sdk-core-*.jar | sed -e "s/.*-sdk-core-//g" | sed -e "s/\.jar//g" > /tmp/version.txt
	curl -o $(JAR_DIR)/aws-java-sdk-sts-`cat /tmp/version.txt`.jar http://central.maven.org/maven2/com/amazonaws/aws-java-sdk-sts/`cat /tmp/version.txt`/aws-java-sdk-sts-`cat /tmp/version.txt`.jar


all: build test

test: $(PKGS)
$(PKGS): golang-test-all-deps
	$(call golang-test-all,$@)

build:
# disable CGO and link completely statically (this is to enable us to run in containers that don't use glibc)
	@CGO_ENABLED=0 go build -a -installsuffix cgo


run:
	GOOS=linux GOARCH=amd64 make build
	docker build -t kinesis-notifications-consumer .
	@docker run -v /tmp:/tmp \
	-v $(AWS_SHARED_CREDENTIALS_FILE):$(AWS_SHARED_CREDENTIALS_FILE) \
	--env-file=<(echo -e $(_ARKLOC_ENV_FILE)) \
	kinesis-notifications-consumer


install_deps: golang-dep-vendor-deps
	$(call golang-dep-vendor)
