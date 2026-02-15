this should be a go project with a client that is a vite plugin.

a basic go websocket server that the client connects to.

when connected, the client supplies a id, which is a subdomain so it should not include "." and other checks, and a port.

the server maintains a list of all the subdomains and ports that are connected to it.

when a client disconnects, the server removes the subdomain and port from its list.

the server shares a docker volume with a traefik reverse proxy.

the reverse proxy is configured to listen to the changes of a specific directory in the docker volume.

when a change is detected, the reverse proxy reloads the configuration.

the job of the go server is to generate a configuration file for the reverse proxy.

File
The file provider lets you define the install configuration in a YAML file.

It supports providing configuration through a single configuration file or multiple separate files.

Configuration Example
You can configure the file provider as detailed below:

```yaml
http:
  routers:
    sub-localhost:
      entryPoints:
        - web
      rule: Host(`<id/subdomain>.localhost`)
      service: local-3000

  services:
    local-3000:
      loadBalancer:
        servers:
          - url: http://localhost:3000
```

also, write a example compose file that builds the go server and starts it along with the reverse proxy.
