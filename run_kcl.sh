#!/usr/bin/env bash
set -e

# For running locally with an AIM role
if [ "$AWS_SHARED_CREDENTIALS_FILE" != "" ] ; then
    export AWS_CREDENTIAL_PROFILES_FILE=$AWS_SHARED_CREDENTIALS_FILE
fi
command -v java >/dev/null 2>&1 || { echo >&2 "Java not installed. Install java!"; exit 1; }
exec java -cp "jars/*"  com.amazonaws.services.kinesis.multilang.MultiLangDaemon consumer.properties
