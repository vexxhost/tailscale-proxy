# `tailscale-proxy`

This is a small daemon which uses `tsnet` to create virtual devices in your
tailnet, which by default will proxy all TCP traffic into the target system
and any ports using `https` will be using the Tailscale issued certificate.

## Configuration

In order to configure it, you will need to setup a configuration file such
as the following:

```yaml
endpoints:
  - hostname: bmc-example-server
    ip: 1.2.3.4
    tags:
      - bmc
      - example
    listeners:
      - port: 443
        type: tls
      - port: 5900
        type: tls
```

In the example above, a Tailscale device will be registered with the name
of `bmc-example-server` and all of it's traffic will be proxied to the IP
address `1.2.3.4`.  The tags assigned to it within Tailscale will be both
`bmc` and `example`.  In addition, the port `443` and `5900` will be proxied
using a Tailscale issued certificate (through LetsEncrypt).
