# Public development service sharing

dev-reverse-proxy makes services on the owner's development machines available at public URLs.

## Language

**Gateway**:
The public entry point through which visitors reach shared development services.

**Local service**:
An application running on a development machine that the owner wants to share.

**Publication**:
The association between a public name and a local service made available through the gateway.
_Avoid_: Client, when referring to the published service rather than the CLI process.

**Publication name**:
The single label that identifies a publication across all development machines, such as `myapp`.

**Wildcard publication**:
A publication available at its own hostname and at hostnames one label beneath it, all sharing the same local service.

**Development machine**:
A machine owned by the operator on which a local service runs.

**Visitor**:
A person or external system accessing a publication through its public URL.
