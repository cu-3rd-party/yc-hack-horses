# Статика хакатона: nginx-unprivileged слушает 8080 не-root (в k8s чарт ставит
# runAsUser 10001), pid и temp в /tmp — в образе ничего не пишется.
# Файлы приходят hostPath'ом в /data (mount перекрывает каталог из образа).
FROM nginxinc/nginx-unprivileged:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
# /data нужен только для локального смок-теста (exec создаёт там файлы);
# в k8s hostPath перекрывает каталог из образа
USER root
RUN mkdir -p /data && chown 101:101 /data
USER 101
EXPOSE 8080
