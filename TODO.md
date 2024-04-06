# TODO.md

### Todo

- [ ] Create an index for existing cached layers on VFS
- [ ] Reduce duplication of HTTP request creation to registries
- [ ] Move from WaitGroups to channels to have finer control over concurrent layer downloads when fetching
- [ ] Proper checking of the returned digest format, i.e 'sha256:abcdef1234'
- [ ] Support OCI image manifests
- [ ] Improve image caching to allow for true in-memory layer caching
- [ ] Use the pivot_root syscall instead of chroot

### Done ✓

- [x] Complete stages 1-6 on codecrafters.io
