// electron-builder: macOS dmg + zip (universal), Windows nsis, Linux AppImage + deb,
// published to GitHub Releases for electron-updater. Signing and notarization run
// only when their secrets are present (see README.md, "Release").

const env = process.env;
const { version } = require("./package.json");

// Notarization: an App Store Connect API key (APPLE_API_KEY is the path to the
// .p8), or an Apple ID with an app-specific password.
const notarize =
  !!(env.APPLE_API_KEY && env.APPLE_API_KEY_ID && env.APPLE_API_ISSUER) ||
  !!(env.APPLE_ID && env.APPLE_APP_SPECIFIC_PASSWORD && env.APPLE_TEAM_ID);

// Windows: Azure Trusted Signing when its account is configured (the
// AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET credentials are read
// from the environment by the signing module), else a certificate (WIN_CSC_LINK).
const azureSignOptions =
  env.AZURE_TRUSTED_SIGNING_ENDPOINT && env.AZURE_TRUSTED_SIGNING_ACCOUNT && env.AZURE_TRUSTED_SIGNING_PROFILE && env.AZURE_TRUSTED_SIGNING_PUBLISHER
    ? {
        publisherName: env.AZURE_TRUSTED_SIGNING_PUBLISHER,
        endpoint: env.AZURE_TRUSTED_SIGNING_ENDPOINT,
        codeSigningAccountName: env.AZURE_TRUSTED_SIGNING_ACCOUNT,
        certificateProfileName: env.AZURE_TRUSTED_SIGNING_PROFILE,
      }
    : undefined;

// Every prerelease (1.2.0-beta.1, 1.2.0-rc.1) is published on the beta channel
// (beta.yml, beta-mac.yml, beta-linux*.yml). A stable version writes its own
// latest*.yml and the beta files too, so beta users get stable releases that are
// newer than their beta.
const channel = /-/.test(version) ? "beta" : "latest";

/** @type {import("electron-builder").Configuration} */
module.exports = {
  appId: "sh.opendesk.desktop",
  productName: "opendesk",
  copyright: "Copyright © opendesk",
  directories: { output: "release", buildResources: "build" },
  files: ["dist/**", "!dist/**/*.map", "package.json"],
  asar: true,
  // Hardening of the shipped Electron binary: no running it as Node, no NODE_OPTIONS or
  // --inspect, cookies (refresh tokens) encrypted at rest with the OS keychain, and app
  // code only from the integrity-checked app.asar.
  electronFuses: {
    runAsNode: false,
    enableCookieEncryption: true,
    enableNodeOptionsEnvironmentVariable: false,
    enableNodeCliInspectArguments: false,
    enableEmbeddedAsarIntegrityValidation: true,
    onlyLoadAppFromAsar: true,
  },
  electronLanguages: ["en", "en-US", "en-GB"],
  protocols: [{ name: "opendesk", schemes: ["opendesk"] }],
  artifactName: "opendesk-${version}-${os}-${arch}.${ext}",
  // GitHub Releases, read through a fixed URL: the repository's "latest" release
  // belongs to the server (v* tags), so each desktop release (desktop-v*) also
  // refreshes the rolling `desktop-latest` release, which is what installed apps
  // check (.github/workflows/desktop.yml).
  publish: [{ provider: "generic", url: env.OPENDESK_UPDATE_URL || "https://github.com/useopendesk/opendesk/releases/download/desktop-latest", channel }],
  generateUpdatesFilesForAllChannels: true,

  mac: {
    category: "public.app-category.business",
    icon: "build/icon.png",
    target: [
      { target: "dmg", arch: ["universal"] },
      { target: "zip", arch: ["universal"] },
    ],
    hardenedRuntime: true,
    gatekeeperAssess: false,
    entitlements: "build/entitlements.mac.plist",
    entitlementsInherit: "build/entitlements.mac.plist",
    darkModeSupport: true,
    // Without CSC_LINK the build is unsigned (local and fork builds).
    identity: env.CSC_LINK ? undefined : null,
    notarize,
    extendInfo: { NSUserNotificationAlertStyle: "banner" },
  },
  dmg: { artifactName: "opendesk-${version}-mac.${ext}" },

  win: {
    icon: "build/icon-win.png",
    target: [{ target: "nsis", arch: ["x64", "arm64"] }],
    ...(azureSignOptions ? { azureSignOptions } : {}),
  },
  nsis: {
    oneClick: true,
    perMachine: false,
    artifactName: "opendesk-${version}-setup.${ext}",
    shortcutName: "opendesk",
  },

  linux: {
    icon: "build/icons",
    category: "Office",
    synopsis: "Shared inboxes, live chat and a knowledge base",
    maintainer: "opendesk <team@opendesk.sh>",
    target: [
      { target: "AppImage", arch: ["x64", "arm64"] },
      { target: "deb", arch: ["x64", "arm64"] },
    ],
    mimeTypes: ["x-scheme-handler/opendesk"],
  },
};
