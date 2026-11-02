# opendesk for iPhone

The SwiftUI app uses the same API as the web app. Its bundle ID is `sh.opendesk.app`.
See [the native contract](../contracts/native.md) for authentication, push, and client behaviour.

## Build checks

The required CI status builds the app for the simulator in both Debug and
Release configurations. The **iOS** workflow can also run these checks manually.
These builds do not use Apple signing credentials.

## Release to TestFlight

A version tag such as `v0.4.6` uploads the matching iOS build to TestFlight from the
**iOS** workflow, beside the server and desktop release. The release never waits for
it: a signing problem or an Apple outage holds back the iPhone build only, and
re-running the iOS workflow on the tag uploads it later. Apple processes each upload
before testers can install it; external testing follows Apple's beta review rules.

The marketing version is the tag's numeric version (`v0.4.6-beta.1` builds `0.4.6`).
The build number is the iOS workflow's run number and attempt, so a rerun makes a
new build.

Repository Actions settings:

- Variable `IOS_TEAM_ID`: the Apple Developer team that owns `sh.opendesk.app`.
- Secrets `APPLE_API_KEY`, `APPLE_API_KEY_ID` and `APPLE_API_ISSUER`: the App Store
  Connect team API key that also notarizes the desktop apps. `APPLE_API_KEY` holds
  the base64-encoded `.p8`. Uploading and automatic signing need the Admin role, or
  App Manager with access to certificates, identifiers and profiles.

The app record and bundle ID belong to that team. Xcode signs automatically in an
isolated keychain on the runner. The exported app must pass code-signature
verification and carry production push and App Attest entitlements before it is
validated or uploaded.

To check signing without uploading, run the **iOS** workflow on a branch with
`version=0.4.6` and `upload=false`. Leave `version` empty for a simulator build.

Release builds use production push and App Attest entitlements. The server side
(APNs key, App Attest app ids, the app-site association) is in
[push notifications](../docs/src/content/docs/self-hosting/push-notifications.md)
and `deploy/terraform/README.md`.
