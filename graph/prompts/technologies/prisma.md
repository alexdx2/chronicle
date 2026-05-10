# PRISMA EXTRACTION RULES

Apply to `.prisma` files or files explicitly marked as Prisma schema.

Extract every model and enum. This is critical — missing models = missing data layer.

```json
{"kind":"model","to":"User"}
{"kind":"enum","to":"OrderStatus"}
{"kind":"model_relation","from":"Order","to":"User"}
```

## Rules

- One `model` fact for every `model X {}` block
- One `enum` fact for every `enum X {}` block
- One `model_relation` for each field that references another model type
- For list relations: `posts Post[]` => `{"kind":"model_relation","from":"User","to":"Post"}`
- For `@relation` decorators: emit relation from declaring model to referenced model
- Do NOT emit relations for scalar fields (String, Int, Boolean, DateTime, etc.)
- Do NOT emit `uses_model` from schema definitions (that's for code that queries models)

## Prisma model usage in code

When you see `this.prisma.user.findMany()` or `prisma.order.create()`:
- The table name after `prisma.` is the model name (lowercase to PascalCase: `user` -> `User`)
- Emit `{"kind":"uses_model","to":"User"}`
