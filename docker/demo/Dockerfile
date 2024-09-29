FROM nginx:1.26.1
COPY htdocs /var/lib/data/demo/htdocs
COPY init /init
COPY nginx.conf /etc/nginx/conf.d/default.conf
CMD ["/init", "nginx", "-g", "daemon off;"]
