import nextConfig from "eslint-config-next";

export default [
  ...nextConfig,
  {
    ignores: [".next/**", "node_modules/**", "coverage/**"],
    rules: {
      "react-hooks/immutability": "off",
      "react-hooks/purity": "off",
      "react-hooks/set-state-in-effect": "off",
    },
  },
];
