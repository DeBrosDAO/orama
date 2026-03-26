## Summary

<!-- What does this PR do? Keep it to 1-3 bullet points. -->

## Motivation

<!-- Why is this change needed? Link to an issue if applicable. -->

## Test plan

<!-- How did you verify this works? -->

- [ ] `make test` passes
- [ ] Tested on sandbox/staging environment

## Distributed system impact

<!-- Does this change affect any of the following? If yes, explain. -->

- [ ] Raft quorum / RQLite
- [ ] WireGuard mesh / networking
- [ ] Olric gossip / caching
- [ ] Service startup ordering
- [ ] Rolling upgrade compatibility

## Checklist

- [ ] Tests added for new functionality or bug fix
- [ ] No debug code (`fmt.Println`, `log.Println`) left behind
- [ ] Docs updated (if user-facing behavior changed)
- [ ] Errors wrapped with context (`fmt.Errorf("...: %w", err)`)
