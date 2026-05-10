# GRAPHQL EXTRACTION RULES

Apply when the file uses GraphQL resolver decorators or schema definitions.

## Resolver methods

`@Query() getUser()` => `{"kind":"endpoint","method":"Query","target":"getUser"}`
`@Mutation() createOrder()` => `{"kind":"endpoint","method":"Mutation","target":"createOrder"}`
`@Subscription() onOrder()` => `{"kind":"endpoint","method":"Subscription","target":"onOrder"}`

## Field resolvers

`@ResolveField()` methods that call services => emit `calls_service`, not endpoint.

## Schema files (.graphql, .gql)

Extract Query/Mutation/Subscription type fields as endpoints:
`type Query { getUser(id: ID!): User }` => `{"kind":"endpoint","method":"Query","target":"getUser"}`

Do NOT emit model facts for GraphQL object types unless they map directly to persistence models.
