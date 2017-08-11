#!/usr/bin/env bash
set -e

# Generate consumer properties
: "${KINESIS_STREAM_NAME?Missing env-var}"
: "${KINESIS_AWS_REGION?Missing env-var}"
: "${KINESIS_APPLICATION_NAME?Missing env-var}"
: "${KINESIS_INITIAL_POSITION?Missing env-var}"

cp consumer.properties.template consumer.properties
sed -i "s/<STREAM_NAME>/${KINESIS_STREAM_NAME}/" consumer.properties
sed -i "s/<REGION_NAME>/${KINESIS_AWS_REGION}/" consumer.properties
sed -i "s/<APPLICATION_NAME>/${KINESIS_APPLICATION_NAME}/" consumer.properties
sed -i "s/<INITIAL_POSITION>/${KINESIS_INITIAL_POSITION}/" consumer.properties


# For running locally with an AIM role
if [ "$AWS_SHARED_CREDENTIALS_FILE" != "" ] ; then
    export AWS_CREDENTIAL_PROFILES_FILE=$AWS_SHARED_CREDENTIALS_FILE
fi
command -v java >/dev/null 2>&1 || { echo >&2 "Java not installed. Install java!"; exit 1; }
exec java -cp "jars/*"  com.amazonaws.services.kinesis.multilang.MultiLangDaemon consumer.properties
