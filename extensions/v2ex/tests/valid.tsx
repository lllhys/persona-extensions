import { View, Section, Text, Row, callback, type Node } from "@persona/extension/ui";
export const valid: Node = <View key="page" title="Synthetic"><Section key="section">
  <Text key="body" text="Synthetic content" style="body" />
  <Row key="row" title="Read" action={callback(() => ({ push: { ui: "topics", state: "{}" } }), { push: { ui: "topics", state: "{}" } })} />
</Section></View>;
