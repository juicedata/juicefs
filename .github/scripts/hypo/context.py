
class Context:
    def __init__(self, root_dir:str, mp:str, volume:str, uuid:str, conf_dir:str, cache_dir:str, gateway_address:str) -> None:
        self.root_dir = root_dir
        self.mp = mp
        self.volume = volume
        self.uuid = uuid
        self.conf_dir = conf_dir
        self.cache_dir = cache_dir
        self.gateway_address = gateway_address