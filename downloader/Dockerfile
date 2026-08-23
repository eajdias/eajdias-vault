FROM mikf123/gallery-dl:latest

COPY strip-ext.sh /app/strip-ext.sh
COPY entrypoint.sh /app/entrypoint.sh
COPY autosync.sh /app/autosync.sh
COPY gallery-dl.conf /app/gallery-dl.conf

RUN chmod +x /app/strip-ext.sh /app/entrypoint.sh /app/autosync.sh

WORKDIR /data
ENTRYPOINT ["/app/entrypoint.sh"]
