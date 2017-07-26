#!/usr/bin/env bash
set -eu

command -v java >/dev/null 2>&1 || { echo >&2 "Java not installed. Install java!"; exit 1; }
exec java -cp \
jars/com/amazonaws/amazon-kinesis-client/1.7.2/amazon-kinesis-client-1.7.2.jar:\
jars/com/amazonaws/aws-java-sdk-dynamodb/1.11.14/aws-java-sdk-dynamodb-1.11.14.jar:\
jars/com/amazonaws/aws-java-sdk-s3/1.11.14/aws-java-sdk-s3-1.11.14.jar:\
jars/com/amazonaws/aws-java-sdk-kms/1.11.14/aws-java-sdk-kms-1.11.14.jar:\
jars/com/amazonaws/aws-java-sdk-core/1.11.14/aws-java-sdk-core-1.11.14.jar:\
jars/commons-logging/commons-logging/1.1.3/commons-logging-1.1.3.jar:\
jars/org/apache/httpcomponents/httpclient/4.5.2/httpclient-4.5.2.jar:\
jars/org/apache/httpcomponents/httpcore/4.4.4/httpcore-4.4.4.jar:\
jars/commons-codec/commons-codec/1.9/commons-codec-1.9.jar:\
jars/com/fasterxml/jackson/core/jackson-databind/2.6.6/jackson-databind-2.6.6.jar:\
jars/com/fasterxml/jackson/core/jackson-annotations/2.6.0/jackson-annotations-2.6.0.jar:\
jars/com/fasterxml/jackson/core/jackson-core/2.6.6/jackson-core-2.6.6.jar:\
jars/com/fasterxml/jackson/dataformat/jackson-dataformat-cbor/2.6.6/jackson-dataformat-cbor-2.6.6.jar:\
jars/joda-time/joda-time/2.8.1/joda-time-2.8.1.jar:\
jars/com/amazonaws/aws-java-sdk-kinesis/1.11.14/aws-java-sdk-kinesis-1.11.14.jar:\
jars/com/amazonaws/aws-java-sdk-cloudwatch/1.11.14/aws-java-sdk-cloudwatch-1.11.14.jar:\
jars/com/google/guava/guava/18.0/guava-18.0.jar:\
jars/com/google/protobuf/protobuf-java/2.6.1/protobuf-java-2.6.1.jar:\
jars/commons-lang/commons-lang/2.6/commons-lang-2.6.jar \
com.amazonaws.services.kinesis.multilang.MultiLangDaemon \
consumer.properties
