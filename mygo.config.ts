const signingIdentity = process.env.MACOS_SIGNING_IDENTITY?.trim() || "-";
const notaryKeychain = process.env.NOTARY_KEYCHAIN?.trim();
const notaryProfile =
  process.env.MACOS_NOTARY_PROFILE?.trim() || (notaryKeychain ? "notary" : "");

export default {
  name: "MCP Gateway",
  slug: "mcp-gateway",
  identifier: "NaichengDeng.mcp-gateway",
  version: "0.0.1",
  copyright: "© 2026 Naicheng Deng",
  license: "GPL-3.0",
  main: "./cmd/gateway-desktop",
  out: "build",
  icon: "cmd/gateway-desktop/assets/icon.png",
  updates: {
    publicKey: "p72p1f7TV6sw5nqnf1Sx3eZAXWUGdbWWz12TXqNpGp0=",
    github: "DengNaichen/mcp-gateway",
  },
  macos: {
    minimumSystemVersion: "12.0",
    dmgTitle: "MCP Gateway",
    signingIdentity,
    ...(signingIdentity !== "-" && notaryProfile
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
