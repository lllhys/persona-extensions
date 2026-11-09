import { View, Section, Text, Row } from "@persona/extension/ui";
export const invalid = <View key="page"><Section key="section">
  <Text key="unsupported" text="Synthetic" unsupported="no" />
  <Text key="number" text={42} />
  <Row title="Missing key" />
</Section></View>;
