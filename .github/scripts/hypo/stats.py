def singleton(cls):
    instances = {}
    def get_instance(*args, **kwargs):
        if cls not in instances:
            instances[cls] = cls(*args, **kwargs)
        return instances[cls]
    return get_instance

@singleton
class Statistics:
    def __init__(self):
        self.stats = {}

    def success(self, function_name):
        if function_name not in self.stats:
            self.stats[function_name] = {'success': 0, 'failure': 0}
        self.stats[function_name]['success'] += 1

    def failure(self, function_name):
        if function_name not in self.stats:
            self.stats[function_name] = {'success': 0, 'failure': 0}
        self.stats[function_name]['failure'] += 1

    def get(self):
        return self.stats