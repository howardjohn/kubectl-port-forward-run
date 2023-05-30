# kubectl port-forward-run

`kubectl port-forward-run` is a plugin to help execute a command with a port-forward established.

## Install

`go install github.com/howardjohn/kubectl-port_forward_run@latest`

## Usage

Example output:

```
$ kubectl port-forward-run pod/some-pod 80 -- curl localhost:{}
```

This will port-forward to `some-pod` on port 80 and execute the `curl` command.
The `{}` will be replaced with the automatically assigned local port.
