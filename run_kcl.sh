#!/usr/bin/env bash
set -eu

export AWS_CREDENTIAL_PROFILES_FILE=$AWS_SHARED_CREDENTIALS_FILE
command -v java >/dev/null 2>&1 || { echo >&2 "Java not installed. Install java!"; exit 1; }
exec java -cp "jars/*"  com.amazonaws.services.kinesis.multilang.MultiLangDaemon consumer.properties
