import { Row, Link, Section, View } from "@persona/extension/ui";
export const invalid = <View key="bad"><Section key="s">
  <Row key="row" title="Bad" unsupported="not in schema" />
  <Link key="link" title="Bad" url={123} />
  <Row title="Missing required stable key" />
</Section></View>;
