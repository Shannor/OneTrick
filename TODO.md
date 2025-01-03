# TODO List

- Need to figure out how to get a session of games for a day
  -  Request: Destiny2.GetActivityHistory 
  - /Platform/Destiny2/2/Account/4611686018434106050/Character/2305843009261519028/Stats/Activities/?page=1&mode=70&count=10 
  - Returns the list of recent activities based on the mode
- Need to figure out how to get guns for that session
  - /Platform/Destiny2/Stats/PostGameCarnageReport/15790919881/
  - Based on the previous results data to give where you use the instance id on the data
  - Will need to find your particular player out of the list, though it shows for everyone.
  - Will be in the extended section, entries > player > values | extended
- Need to figure out if it's possible to see the rolls on which weapons were used. 
  - Request: Destiny2.GetProfile with ProfileInventories will dump all data around what's being held and what's in the vault.  




Steps of actions:
1. Get users account / Possibly sign in is a requirement
2. Get current loadouts when ready to save information about guns and classes.
   3. Need to have users OAuth token for that.
3. Need to grab information before the game.
   4. Keep timestamps
5. We may still poll during the game to keep track of live games.
   6. And can use this to see if something changes. Though still doesn't work for multiples of the same gun
7. Need save game information (probably)
8. Need to have a way to stop it.

Notes:
- Need to add auto generating for enpoints https://github.com/Bungie-net/api/blob/master/openapi.json
- https://github.com/oapi-codegen/oapi-codegen?tab=readme-ov-file#generating-api-clients






What to build for the simplest idea. 
1. Get my OAuth Token.
2. Run Go server locally
3. Add a cron job that will check every 2 minutes. 
4. Pull down the data and store it in memory/write to disk maybe
5. See if the data is useful and the logic worked correctly.
6. Then try to see the data somehow in UI.
