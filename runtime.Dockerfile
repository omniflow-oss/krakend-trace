# runtime.Dockerfile
ARG KRKN_VERSION=2.10.1

FROM krakend:${KRKN_VERSION}

ENV KRKN_PLUGIN_FOLDER=/etc/krakend/plugins
USER root

RUN mkdir -p ${KRKN_PLUGIN_FOLDER}
COPY .dist/trace-plugin.so ${KRKN_PLUGIN_FOLDER}/trace-plugin.so

# Optional: add gateway configuration
# COPY config/krakend.json /etc/krakend/krakend.json

USER krakend
ENTRYPOINT ["krakend"]
CMD ["run", "--config", "/etc/krakend/krakend.json"]