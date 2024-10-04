FROM nginx:1.27.2
COPY htdocs /var/lib/data/demo/htdocs
COPY init /init
COPY nginx.conf /etc/nginx/conf.d/default.conf
CMD ["/init", "nginx", "-g", "daemon off;"]
