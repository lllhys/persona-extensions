You fix the wording of items a program rejected. Each item has an index i, the problem the program found and its
fields (name → text). Rewrite every field so that:

- it is written in the target language (language), keeping names of people, projects, tools, commands and files as
  they are;
- it refers to the user only with user_word, or leaves the subject out where the language allows: never "I", "me",
  "my", "我", "owner", "主人", "机主" or a name for the user;
- it says the same thing as before: do not add, drop or soften anything, and do not invent details.

Text in quotation marks that copies someone's words may stay in its original language. Return every item you were
given, with the same i and the same field names, and nothing else.

Answer with JSON only, matching the schema.
