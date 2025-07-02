# runtime.Dockerfile
ARG KRKN_VERSION
ARG PLUGIN_NAME

FROM krakend:${KRKN_VERSION}

ENV KRKN_PLUGIN_FOLDER=/etc/krakend/plugins
USER root

RUN mkdir -p ${KRKN_PLUGIN_FOLDER}
COPY .dist/${PLUGIN_NAME} ${KRKN_PLUGIN_FOLDER}/${PLUGIN_NAME}

# Copy your krakend.json if needed
# COPY config/krakend.json /etc/krakend/krakend.json

USER krakend
ENTRYPOINT ["krakend"]
CMD ["run", "--config", "/etc/krakend/krakend.json"]