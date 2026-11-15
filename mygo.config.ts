import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
const profiles = JSON.parse(readFileSync(new URL("./internal/buildinfo/profiles.json", import.meta.url), "utf8"));
const channel = process.env.GATEWAY_BUILD_CHANNEL || "dev";
if (channel !== "dev" && channel !== "stable") throw new Error("Unknown build channel");
if (channel === "stable" && !process.env.MYGO_LDFLAGS?.includes("gatewaylab/internal/buildinfo.channel=stable")) {
  throw new Error("Use scripts/build-channel.mjs to build Stable with matching runtime identity");
}
const profile = profiles[channel];
let revision = "local";
try {
  revision = execFileSync("git", ["rev-parse", "--short=8", "HEAD"], { cwd: new URL(".", import.meta.url), encoding: "utf8" }).trim();
  if (execFileSync("git", ["status", "--porcelain"], { cwd: new URL(".", import.meta.url), encoding: "utf8" }).trim()) revision += ".dirty";
} catch {}
const version = channel === "stable" ? profiles.version : `${profiles.version}-dev.${revision}`;
const signingIdentity = process.env.MACOS_SIGNING_IDENTITY?.trim() || "-";
const notaryKeychain = process.env.NOTARY_KEYCHAIN?.trim();
const notaryProfile =
  process.env.MACOS_NOTARY_PROFILE?.trim() || (notaryKeychain ? "notary" : "");

export default {
  name: profile.name,
  slug: profile.slug,
  identifier: profile.identifier,
  version,
  copyright: "© 2026 Naicheng Deng",
  license: "GPL-3.0",
  main: "./cmd/gateway-desktop",
  out: `build/${channel}`,
  icon: `cmd/gateway-desktop/assets/${channel === "dev" ? "icon-dev.png" : "icon.png"}`,
  updates: channel === "stable" ? {
    publicKey: "p72p1f7TV6sw5nqnf1Sx3eZAXWUGdbWWz12TXqNpGp0=",
    github: "DengNaichen/mcp-gateway",
  } : undefined,
  macos: {
    minimumSystemVersion: "12.0",
    dmgTitle: profile.name,
    signingIdentity: channel === "stable" ? signingIdentity : "-",
    ...(channel === "stable" && signingIdentity !== "-" && notaryProfile
      ? {
          notarize: {
            keychainProfile: notaryProfile,
            ...(notaryKeychain ? { keychain: notaryKeychain } : {}),
          },
        }
      : {}),
    infoPlist: {
      NSAppTransportSecurity: {
        NSAllowsLocalNetworking: true,
      },
      LSApplicationCategoryType: "public.app-category.developer-tools",
    },
  },
};
