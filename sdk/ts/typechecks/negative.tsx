import { Button, Text, View, Html, callback, mount } from "@persona/extension/ui";

// Deliberately invalid fixtures. The separate compiler runner requires every line to fail.
const dom = <div />;
const unknown = <UnknownNative key="unknown" />;
const wrongText = <Text key="text" text={42} />;
const missingKey = <Text text="fixture" />;
const unknownProp = <Text key="text" text="fixture" arbitrary={true} />;
const rawCallback = <Button key="button" title="fixture" action={() => {}} />;
const missingAction = <Button key="button" title="fixture" />;
const clientStyle = <Button key="button" title="fixture" action={{ id: "native" }} style={{ color: "red" }} />;
const assistantPages = <View key="root" pages={{}} />;
const assistantAction = <Button key="button" title="fixture" action={{ id: "native", reply: "fixture" }} />;
const missingParser = callback<number>(value => value, {});
const missingAuthorization = mount(<View key="root" />, {});
void [dom, unknown, wrongText, missingKey, unknownProp, rawCallback, missingAction, clientStyle, assistantPages, assistantAction, missingParser, missingAuthorization, Html];
