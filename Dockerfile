FROM openjdk:8-jre

# install `make`
RUN apt-get -y update && apt-get install -y -q build-essential

ADD jars jars
ADD consumer.properties.template .
ADD run_kcl.sh .
ADD kinesis-notifications-consumer kinesis-notifications-consumer
ADD kvconfig.yml kvconfig.yml

ENTRYPOINT ["/bin/bash", "./run_kcl.sh"]
